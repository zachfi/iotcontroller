# Proposal: Cache the router kubeclient to drop apiserver QPS

Status: proposal — not yet implemented.
Filed by: deployment_tools session, 2026-05-09, against branch `main` at
commit `571e61f2`.

## Observation (from production)

In the znet homelab cluster, the `iot.iot/v1` API group dominates the kss
control-plane apiserver load:

| rps  | verb / resource              | scope     |
| ---: | ---------------------------- | --------- |
| 25.5 | `GET iot.iot/v1/devices`     | resource  |
| 16.6 | `LIST iot.iot/v1/conditions` | namespace |
|  2.0 | `PUT iot.iot/v1/devices`     | resource  |
|  0.3 | `PATCH iot.iot/v1/devices`   | resource  |
|  ~0  | `WATCH iot.iot/v1/*`         | —         |

Total: **~42 rps from this single CRD group**, ≈14% of the cluster's entire
apiserver request rate (288 rps total). The **near-zero WATCH rate** is the
tell — a controller using the controller-runtime informer cache reads exclusively
from the cache and only WATCHes once per type. Direct GET/LIST per reconcile is
the signature of a non-cached client.

The kss nodes (k3s control-plane, 2 vCPU each) are pegged 80–99 % busy, with
etcd peer round-trip p99 over 3 s and `KubeAPIErrorBudgetBurn` firing.
Eliminating this 42 rps is one of the largest single CPU wins available
without new hardware.

## Root cause

`cmd/app/modules.go::initKubeClient`:

```go
func (a *App) initKubeClient() (services.Service, error) {
    scheme := runtime.NewScheme()
    utilruntime.Must(clientgoscheme.AddToScheme(scheme))
    utilruntime.Must(iotv1.AddToScheme(scheme))

    cfg, err := config.GetConfig()
    if err != nil {
        return nil, err
    }

    c, err := client.New(cfg, client.Options{Scheme: scheme})  // ← uncached
    if err != nil {
        return nil, err
    }

    c = client.NewNamespacedClient(c, a.cfg.Controller.Namespace)
    a.kubeclient = c
    return services.NewIdleService(nil, nil), nil
}
```

`client.New(cfg, client.Options{...})` returns a **direct API client** with
no cache. Every `Get`/`List` against it is a round-trip to the apiserver.

That `a.kubeclient` is then passed into the **router** (`modules/router/router.go`)
and into the MQTT-driven router implementations:

- `pkg/iot/util/util.go::GetOrCreateAPIDevice` — called per inbound MQTT
  device message; does `kubeclient.Get(ctx, nsn, &device)` → produces the
  25.5 rps `GET devices`.
- `pkg/iot/routers/zigbee2mqtt/zigbee2mqtt.go:320` — `kubeclient.List(ctx, list, InNamespace("iot"))`.
- `pkg/iot/routers/nativezigbee/nativezigbee.go:196,326,354` — three
  `kubeclient.List(...)` call sites; collectively the 16.6 rps
  `LIST conditions` (and any `LIST devices` too).

For comparison, the **controllers** (`modules/controller/controller.go:108,119`)
use `c.mgr.GetClient()`, which is the manager's cached client — so reconciles
themselves don't hit the apiserver. Only the router path is uncached.

## Fix options

### Option 1 (recommended): reuse the manager's cached client

The controller manager already runs informers for `iotv1.Device`, `iotv1.Zone`,
and `iotv1.Condition` (the controllers register `For(&iotv1.X{})`). The router
just needs the same `mgr.GetClient()` instead of a fresh `client.New`.

Skeleton:

```go
// In modules/controller/controller.go (or wherever the manager lives), expose
// the manager so other modules can read its client + cache:
func (c *Controller) Manager() ctrl.Manager { return c.mgr }

// In cmd/app/modules.go, drop initKubeClient as the source of a.kubeclient
// and instead pull from the controller after it's started:
func (a *App) initRouter() (services.Service, error) {
    // ...
    a.kubeclient = a.controller.Manager().GetClient()
    // optional: keep client.NewNamespacedClient wrapping if router needs it
    r, err := router.New(a.cfg.Router, a.logger, a.kubeclient, ...)
    // ...
}
```

This requires `router` to be initialised *after* `controller`, which is
already true in the existing module ordering.

Pros:
- One cache, one set of informers — no duplicate WATCHes.
- All Get/List for `Device` / `Condition` / `Zone` go through the cache,
  dropping ~42 rps off the apiserver.
- Updates and Status updates still go straight to the apiserver (same as
  today — that's correct behaviour).

Cons:
- Slight coupling: router must run after controller. (Already the case.)
- The cache is populated asynchronously; the router needs to wait for
  `mgr.GetCache().WaitForCacheSync(ctx)` before serving the first MQTT
  message, otherwise early Gets can return NotFound for objects that exist.

### Option 2: a separate `cluster.Cluster` for the router

```go
import "sigs.k8s.io/controller-runtime/pkg/cluster"

cl, err := cluster.New(cfg, func(o *cluster.Options) {
    o.Scheme = scheme
    o.Cache.DefaultNamespaces = map[string]cache.Config{
        "iot": {},
    }
})
go cl.Start(ctx)
cl.GetCache().WaitForCacheSync(ctx)
a.kubeclient = cl.GetClient()
```

Use this if you want a cache that's lifecycle-independent of the controller
manager (for example so the router still works even if the controller module
is disabled).

Pros: decoupled from controller, can scope the cache narrowly (`InNamespace`).
Cons: a second set of informers if the controller is also running — not free.

### Option 3: leave `client.New` but wrap with a delegating client

```go
c, err := client.New(cfg, client.Options{
    Scheme: scheme,
    Cache: &client.CacheOptions{
        Reader: someCache,
    },
})
```

`client.Options.Cache` is the controller-runtime ≥ 0.15 way to attach a cache
to a standalone client. Less plumbing than option 2 but you still have to
construct and start `someCache` somewhere — at which point option 1 or 2 is
clearer.

## Validation

Once deployed, verify by querying the cluster's apiserver metrics:

```promql
# Should drop from ~42 rps to ~0 (or only WATCH-driven background polling)
sum by (verb) (rate(apiserver_request_total{group="iot.iot"}[5m]))

# Should rise from ~0 (one WATCH per type stays open):
sum by (verb) (rate(apiserver_request_total{group="iot.iot",verb="WATCH"}[5m]))
```

The kss CPU and `etcdMemberCommunicationSlow` alerts should improve in
parallel.

## Out of scope

- The receiver / hookreceiver paths: not yet audited; same audit recipe
  applies (`grep -rn "kubeclient.Get\|kubeclient.List"`).
- The Status().Update calls in the routers — those *should* hit the
  apiserver; status writes don't go through the cache.
