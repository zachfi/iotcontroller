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
)

// reinterviewPollInterval is the cadence at which the coordinator
// scans Device CRs for the AnnotationReinterviewRequested annotation.
// Re-interview is an operator-triggered, low-frequency action — a 30s
// tick is fast enough that "kubectl annotate" feels responsive
// without spamming the apiserver.
const reinterviewPollInterval = 30 * time.Second

// runReinterviewPoll periodically scans Device CRs for the reinterview
// annotation and dispatches an interview for any device that has it
// set. Exits cleanly when ctx is cancelled.
//
// Called as a goroutine from running(). If kubeClient is nil (running
// without a k8s connection, e.g. in unit tests) the poll loop is a
// no-op — operator-triggered re-interviews are by definition a
// production-only feature.
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
// dispatches an interview for each. Errors on individual devices don't
// abort the tick — we just log and move on so a single misbehaving
// device doesn't block re-interview for the rest.
//
// Separated from runReinterviewPoll so unit tests can exercise one tick
// without spinning up a ticker.
func (z *ZigbeeCoordinator) reinterviewTick(ctx context.Context) {
	var devices apiv1.DeviceList
	if err := z.kubeClient.List(ctx, &devices); err != nil {
		z.logger.Warn("re-interview poll: failed to list devices", slog.String("error", err.Error()))
		return
	}

	for i := range devices.Items {
		d := &devices.Items[i]
		if d.Annotations[apiv1.AnnotationReinterviewRequested] == "" {
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

		z.logger.Info("re-interview triggered by annotation",
			slog.String("device", d.Name),
			slog.String("network_address", fmt.Sprintf("0x%04x", nwk)),
			slog.String("ieee_address", fmt.Sprintf("0x%016x", ieee)),
			slog.String("requested_by", d.Annotations[apiv1.AnnotationReinterviewRequested]),
		)

		// Dispatch the interview through the same path a join would.
		// On success, interviewAndDispatch forwards a SUCCESSFUL
		// result to the router, which updates the Device CR's Spec
		// with model/vendor/etc. On failure the result is FAILED and
		// the router leaves the Spec untouched (current behaviour).
		// Either way, we clear the annotation here so we don't loop
		// on a permanently-quirky device — the operator can re-set
		// it later to retry.
		z.interviewAndDispatch(ctx, ieee, nwk)

		if err := z.clearReinterviewAnnotation(ctx, d); err != nil {
			z.logger.Warn("re-interview poll: failed to clear annotation; will retry next tick",
				slog.String("device", d.Name),
				slog.String("error", err.Error()),
			)
		}
	}
}

// deviceAddressesFromSpec parses the IEEE and NWK addresses out of a
// Device CR's Spec. Returns an error if either is missing or malformed.
//
// Format: hex strings with a "0x" prefix.
//   - Spec.IEEEAddress: "0x" + 16 hex chars  (64-bit MAC)
//   - Spec.NetworkAddress: "0x" + 4 hex chars (16-bit short address)
//
// The 0x prefix is optional in what we accept (we strip it) but is the
// canonical form used everywhere else in the codebase.
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
		// NWK is required because the dongle's InterviewDevice takes a
		// network address. We could look up NWK from IEEE via the
		// coordinator's nwkToIEEE map, but that fails after a coord
		// restart until the device chirps. Require Spec.NetworkAddress
		// for now; revisit if it becomes a friction point.
		return 0, 0, fmt.Errorf("device has empty Spec.NetworkAddress (re-interview needs it; populated on next message from the device)")
	}
	nwk, err := strconv.ParseUint(nwkStr, 16, 16)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid Spec.NetworkAddress %q: %w", d.Spec.NetworkAddress, err)
	}

	return ieee, uint16(nwk), nil
}

// clearReinterviewAnnotation removes the reinterview annotation from a
// Device CR using a JSON-merge patch. A merge patch with a null value
// for the annotation key tells the apiserver to delete that key while
// leaving the rest of the annotations map intact — avoids the read-
// modify-write race a regular Update would suffer if someone else
// touched the resource between our List and Update.
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
