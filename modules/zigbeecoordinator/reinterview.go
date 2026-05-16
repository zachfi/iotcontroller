package zigbeecoordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/types"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	dongletypes "github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
)

// reinterviewPollInterval is the cadence at which the coordinator scans
// Device CRs for the AnnotationReinterviewRequested annotation. Re-
// interview is operator-triggered and low-frequency; 30s feels
// responsive to "kubectl annotate" without spamming the apiserver.
const reinterviewPollInterval = 30 * time.Second

// opportunisticEnrichTimeout is the per-attempt budget for the Basic
// cluster read once we observe the target device is awake. Brief
// enough that a single missed reply doesn't wedge the coordinator,
// generous enough to absorb routing latency and one parent-buffer
// indirect-tx cycle.
const opportunisticEnrichTimeout = 8 * time.Second

// opportunisticEnrichDelay is the wait between observing activity from
// the target device and dispatching the Basic cluster read. Most
// sleepy end devices stay awake for ~30–250ms after their parent
// indicates a queued message; we need to be in that window. Sending
// the request before the parent has finished processing the device's
// poll can race the parent's buffer. A small delay (50–100ms) gives
// the parent time to flush.
const opportunisticEnrichDelay = 75 * time.Millisecond

// pendingReinterview describes a re-interview waiting for the target
// device to become reachable. Registered when the annotation poll sees
// an annotated Device CR; consumed when the parse path observes any
// incoming message from the device's network address — at which point
// the radio is briefly active and the Basic cluster read has a real
// chance of getting through.
//
// The deviceCRName field is required for clearing the annotation when
// the enrichment completes — it's the same as Device.metadata.name,
// not the IEEE/NWK address.
type pendingReinterview struct {
	ieee         uint64
	nwk          uint16
	deviceCRName string
	requestedBy  string
	registeredAt time.Time
}

// runReinterviewPoll periodically scans Device CRs for the reinterview
// annotation and REGISTERS a pending enrichment for any device that
// has it set. The actual interview work is deferred to whenever the
// parse path next observes a message from the device — see
// triggerPendingEnrichmentIfAny. Exits cleanly when ctx is cancelled.
//
// Called as a goroutine from running(). If kubeClient is nil (test
// wiring without a k8s connection) the poll is a no-op.
func (z *ZigbeeCoordinator) runReinterviewPoll(ctx context.Context) {
	if z.kubeClient == nil {
		z.logger.Debug("re-interview poll disabled (no kubeClient)")
		return
	}

	ticker := time.NewTicker(reinterviewPollInterval)
	defer ticker.Stop()

	z.logger.Info("re-interview poll started", slog.Duration("interval", reinterviewPollInterval))

	for {
		select {
		case <-ctx.Done():
			z.logger.Info("re-interview poll stopped")
			return
		case <-ticker.C:
			z.reinterviewTick(ctx)
		}
	}
}

// reinterviewTick lists Devices with the reinterview annotation and
// registers each as a pendingReinterview keyed by NWK. The enrichment
// itself fires later from triggerPendingEnrichmentIfAny when the parse
// path sees a message from the target NWK — sleepy end devices spend
// most of their time asleep and never answer requests sent during
// that window, so we have to wait for them to come to us.
//
// Errors on individual devices don't abort the tick. Idempotent: an
// already-registered NWK simply gets its requestedBy / registeredAt
// refreshed (re-setting the annotation after an earlier failed pass
// is the operator's retry mechanism).
//
// Separated from runReinterviewPoll so unit tests can exercise one
// tick without spinning a ticker.
func (z *ZigbeeCoordinator) reinterviewTick(ctx context.Context) {
	var devices apiv1.DeviceList
	if err := z.kubeClient.List(ctx, &devices); err != nil {
		z.logger.Warn("re-interview poll: failed to list devices", slog.String("error", err.Error()))
		return
	}

	for i := range devices.Items {
		d := &devices.Items[i]
		val := d.Annotations[apiv1.AnnotationReinterviewRequested]
		if val == "" {
			continue
		}

		ieee, nwk, err := deviceAddressesFromSpec(d)
		if err != nil {
			z.logger.Warn("re-interview poll: device not eligible — leaving annotation set",
				slog.String("device", d.Name),
				slog.String("reason", err.Error()),
			)
			continue
		}

		z.registerPendingReinterview(pendingReinterview{
			ieee:         ieee,
			nwk:          nwk,
			deviceCRName: d.Name,
			requestedBy:  val,
			registeredAt: time.Now(),
		})
		z.logger.Info("re-interview registered; waiting for device activity",
			slog.String("device", d.Name),
			slog.String("network_address", fmt.Sprintf("0x%04x", nwk)),
			slog.String("ieee_address", fmt.Sprintf("0x%016x", ieee)),
			slog.String("requested_by", val),
		)
	}
}

// registerPendingReinterview is idempotent: re-registering the same NWK
// (after the operator re-sets the annotation following a failure) just
// replaces the entry with fresh timestamps.
func (z *ZigbeeCoordinator) registerPendingReinterview(p pendingReinterview) {
	z.pendingReinterviewMu.Lock()
	defer z.pendingReinterviewMu.Unlock()
	if z.pendingReinterview == nil {
		z.pendingReinterview = make(map[uint16]pendingReinterview)
	}
	z.pendingReinterview[p.nwk] = p
}

// triggerPendingEnrichmentIfAny is called from the parse path on every
// inbound message. If the source NWK has a pending re-interview, we
// remove it from the map (so duplicate-trigger races don't spawn
// parallel enrichments) and kick off the Basic cluster read in a
// goroutine. Never blocks the parse goroutine.
//
// The brief opportunisticEnrichDelay before the dispatch gives the
// device's parent router time to finish handling the poll-response
// it just sent us before we put another message in its outbound
// queue. Without this, on busy networks the read can race the parent
// and be dropped before reaching the device.
func (z *ZigbeeCoordinator) triggerPendingEnrichmentIfAny(srcNwk uint16) {
	z.pendingReinterviewMu.Lock()
	p, ok := z.pendingReinterview[srcNwk]
	if ok {
		delete(z.pendingReinterview, srcNwk)
	}
	z.pendingReinterviewMu.Unlock()

	if !ok {
		return
	}

	go func() {
		// Use Background here, not the parse-path ctx — we want this
		// enrichment to outlive the single message dispatch.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		time.Sleep(opportunisticEnrichDelay)

		z.logger.Info("re-interview: device activity observed, dispatching Basic cluster enrichment",
			slog.String("device", p.deviceCRName),
			slog.String("network_address", fmt.Sprintf("0x%04x", p.nwk)),
			slog.String("ieee_address", fmt.Sprintf("0x%016x", p.ieee)),
			slog.String("requested_by", p.requestedBy),
			slog.Duration("waited", time.Since(p.registeredAt)),
		)

		z.enrichFromBasicCluster(ctx, p)
	}()
}

// enrichFromBasicCluster performs a single Basic cluster (0x0000) read
// — manufacturer / model / power source — against the target device
// and dispatches whatever it got back to the router as a SUCCESSFUL
// partial interview. Deliberately skips ZDO entirely: ZDO is what was
// failing for these devices in the first place, and burning the
// device's awake window on ZDO retries would mean the Basic cluster
// step misses the window entirely.
//
// Annotation is cleared on a successful response (regardless of which
// attributes the device returned), so a permanently-quirky device
// doesn't loop. On error the annotation stays set and the operator
// can leave it alone (next message → another attempt) or remove it.
func (z *ZigbeeCoordinator) enrichFromBasicCluster(ctx context.Context, p pendingReinterview) {
	basic, err := z.readBasicClusterAttributes(ctx, p.nwk, 1, opportunisticEnrichTimeout)
	if err != nil {
		z.logger.Warn("re-interview: Basic cluster enrichment failed; annotation stays set for next activity",
			slog.String("device", p.deviceCRName),
			slog.String("network_address", fmt.Sprintf("0x%04x", p.nwk)),
			slog.String("ieee_address", fmt.Sprintf("0x%016x", p.ieee)),
			slog.String("error", err.Error()),
		)
		// Re-register so the next message from the device triggers
		// another attempt. The poll loop would re-add it too on its
		// next tick, but re-adding here closes the window faster.
		z.registerPendingReinterview(p)
		return
	}

	z.logger.Info("re-interview: Basic cluster enrichment succeeded",
		slog.String("device", p.deviceCRName),
		slog.String("manufacturer", basic.Manufacturer),
		slog.String("model", basic.Model),
		slog.Uint64("power_source", uint64(basic.PowerSource)),
	)

	// Dispatch a minimal SUCCESSFUL interview result carrying just the
	// Basic cluster fields. The router's InterviewRoute writes only
	// non-empty fields onto the Device CR Spec, so any previously-
	// populated values (e.g. operator-patched while we were getting
	// our act together) are preserved when the device returns
	// UNSUPPORTED_ATTRIBUTE for a given attr.
	z.sendInterviewResult(ctx, p.ieee, p.nwk, &dongletypes.DeviceInterviewInfo{
		NetworkAddress: p.nwk,
		Manufacturer:   basic.Manufacturer,
		Model:          basic.Model,
		PowerSource:    basic.PowerSource,
	}, nil)

	if err := z.clearReinterviewAnnotationByName(ctx, p.deviceCRName); err != nil {
		z.logger.Warn("re-interview: failed to clear annotation post-enrichment; will retry next poll",
			slog.String("device", p.deviceCRName),
			slog.String("error", err.Error()),
		)
		// Don't re-register — the enrichment succeeded; the annotation
		// will be cleared on the next poll tick that successfully
		// patches the apiserver.
	}
}

// deviceAddressesFromSpec parses the IEEE and NWK addresses out of a
// Device CR's Spec. Returns an error if either is missing or malformed.
//
// Format: hex strings with optional "0x" prefix.
//   - Spec.IEEEAddress: 16 hex chars  (64-bit MAC)
//   - Spec.NetworkAddress: 4 hex chars (16-bit short address)
//
// The canonical form used elsewhere in the codebase includes "0x"; we
// strip it tolerantly so an operator hand-patching the CR can use
// either form.
func deviceAddressesFromSpec(d *apiv1.Device) (uint64, uint16, error) {
	ieeeStr := strings.TrimPrefix(strings.ToLower(d.Spec.IEEEAddress), "0x")
	if ieeeStr == "" {
		return 0, 0, fmt.Errorf("device has empty Spec.IEEEAddress")
	}
	ieee, err := strconv.ParseUint(ieeeStr, 16, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid Spec.IEEEAddress %q: %w", d.Spec.IEEEAddress, err)
	}

	nwkStr := strings.TrimPrefix(strings.ToLower(d.Spec.NetworkAddress), "0x")
	if nwkStr == "" {
		return 0, 0, fmt.Errorf("device has empty Spec.NetworkAddress (re-interview needs it; populated on next message from the device)")
	}
	nwk, err := strconv.ParseUint(nwkStr, 16, 16)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid Spec.NetworkAddress %q: %w", d.Spec.NetworkAddress, err)
	}

	return ieee, uint16(nwk), nil
}

// clearReinterviewAnnotationByName looks up the Device CR by name and
// patches out the reinterview annotation. Used by the opportunistic
// enrichment path, which holds the device name in pendingReinterview
// (not a full Device pointer — by the time enrichment runs, an in-
// memory Device snapshot from the poll tick could be stale).
func (z *ZigbeeCoordinator) clearReinterviewAnnotationByName(ctx context.Context, name string) error {
	var d apiv1.Device
	if err := z.kubeClient.Get(ctx, kubeclient.ObjectKey{Name: name, Namespace: "iot"}, &d); err != nil {
		return fmt.Errorf("getting device %q: %w", name, err)
	}
	return z.clearReinterviewAnnotation(ctx, &d)
}

// clearReinterviewAnnotation patches out the reinterview annotation
// using a JSON-merge patch with a null value (which deletes the key).
// Avoids the read-modify-write race that a regular Update would
// suffer.
func (z *ZigbeeCoordinator) clearReinterviewAnnotation(ctx context.Context, d *apiv1.Device) error {
	patchObj := map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				apiv1.AnnotationReinterviewRequested: nil,
			},
		},
	}
	patchBytes, err := json.Marshal(patchObj)
	if err != nil {
		return fmt.Errorf("marshaling annotation-clear patch: %w", err)
	}

	patch := kubeclient.RawPatch(types.MergePatchType, patchBytes)
	if err := z.kubeClient.Patch(ctx, d, patch); err != nil {
		return fmt.Errorf("patching device %q: %w", d.Name, err)
	}
	return nil
}
