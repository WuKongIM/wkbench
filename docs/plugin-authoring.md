# Plugin Authoring

`wkbench` plugins are executable programs that expose benchmark units through
the `wkbench.plugin/v1` frame protocol over stdin and stdout. The host starts
executables passed with `-plugin` or configured in `.wkbench/plugins.yaml`,
performs a handshake, lists units, and then calls `Validate`, `Plan`, and
`Run` over the same stdio frame stream.

Go plugins use the same `contract.Unit` interface as in-process units through
`sdk/go/wkbench/plugin`.

## Entrypoint

```go
package main

import (
	"os"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/plugins/demo/echo"
	wkplugin "github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

func main() {
	if err := wkplugin.Serve(wkplugin.Plugin{
		Name:    "wkbench.demo",
		Version: "0.1.0",
		Units:   []contract.Unit{echo.Unit{}},
	}, os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
```

`Name` is the plugin name used by scenario YAML when a unit kind is qualified.
`Units` contains ordinary `contract.Unit` implementations.

## Port Metadata

Ports that cross process boundaries should declare `PortMeta` so the host can
reason about data boundaries, transport, reportability, and sensitivity.

```go
contract.PortDef{
	Name: "result",
	Type: "port.demo.echo/v1",
	Meta: contract.PortMeta{
		Boundary:   contract.PortBoundaryData,
		Transport:  contract.PortTransportInline,
		Reportable: true,
		Sensitive:  false,
	},
}
```

Useful fields:

- `Boundary`: use `contract.PortBoundaryData` for serializable values that may
  cross the plugin boundary. Capability and local-resource boundaries should
  not be treated as inline data.
- `Transport`: Phase 1 supports inline JSON values across stdio.
- `Reportable`: allows the output to appear in `report.json` and `summary.md`.
- `Sensitive`: prevents an output from being exposed in reports.
- `Schema`, `Encodings`, `MaxPayloadBytes`, and `Operations`: document the
  expected shape, encoding, size limit, and supported capability operations.

For reportable outputs, values may implement `contract.ReportableOutput`.
The plugin server sends separate raw and report payloads when available. The
host stores the raw output for downstream inputs and exposes only the report
payload in reports. Sensitive outputs are not exposed in reports.

For cross-process inputs in Phase 1, the host enforces the metadata on both
the producing output port and the consuming input port before sending the RPC
request. Both sides must be non-sensitive `PortBoundaryData` ports with
`PortTransportInline`, must allow JSON when `Encodings` is set, and must fit
within `MaxPayloadBytes`. Paged, artifact-ref, stream capability, and
local-resource inputs or producer outputs are rejected until those transports
have explicit protocol support.

## Host Usage

Generate a standalone external plugin project:

```bash
GOWORK=off go run ./cmd/wkbench plugin init \
  -dir /tmp/acme-wkbench-plugin \
  -module example.com/acme/wkbench-plugin \
  -name acme.echo
cd /tmp/acme-wkbench-plugin
go test ./...
go build -o ./bin/acme-echo-plugin ./cmd/acme-echo-plugin
```

Register it in a wkbench project:

```bash
GOWORK=off go run ./cmd/wkbench plugin add acme.echo /tmp/acme-wkbench-plugin/bin/acme-echo-plugin
GOWORK=off go run ./cmd/wkbench plugin list
GOWORK=off go run ./cmd/wkbench plugin doctor
```

After registration, `list-units`, `validate`, `explain`, `plan`, and `run`
automatically load enabled plugins from `.wkbench/plugins.yaml`.

Build and load the demo plugin:

```bash
GOWORK=off go build -o /tmp/wkbench-demo-plugin ./plugins/demo/cmd/wkbench-demo-plugin
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin list-units
```

Validate and run the demo scenario:

```bash
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin validate -scenario ./examples/plugin-echo.yaml
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin run -scenario ./examples/plugin-echo.yaml
```

Build and load the official pure-data plugin:

```bash
GOWORK=off go build -o /tmp/wkbench-official-data-plugin ./plugins/official/dataplane/cmd/wkbench-official-data-plugin
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-official-data-plugin validate -scenario ./examples/official-data-plugin.yaml
```

`-plugin` is a global CLI flag and may be repeated before the subcommand.
It is merged with `.wkbench/plugins.yaml`; duplicate executable paths are
loaded once.

## Bundled Official Plugins

`list-units`, `validate`, `explain`, `plan`, and `run` start bundled official
plugins by default. They use the same stdio RPC path as third-party plugins.
The default official plugins currently expose data and control-plane units:

- `wkbench.official.core`: `core.static_groups/v1`
- `wkbench.official.identity`: `identity.pool/v1`,
  `identity.person_pairs/v1`
- `wkbench.official.wukongim`: `wukongim.target/v1`,
  `wukongim.prepare_group_channels/v1`
- `wkbench.official.report`: `report.assert/v1`

The host still keeps local units whose ports are Go capabilities, local
resources, or background lifecycles: fake senders, traffic generators,
`wkproto.session_pool/v1`, `wukongim.prepare_tokens/v1`, and
`wukongim.metrics_collector/v1`. These need richer RPC support before they can
move safely.

Use `-no-official-plugins` before the command to inspect or run only the
host-local registry:

```bash
GOWORK=off go run ./cmd/wkbench -no-official-plugins list-units
```

## Project Plugin Config

`wkbench` discovers project plugin config by walking upward from the current
directory to `.wkbench/plugins.yaml`.

```yaml
version: wkbench.plugins/v1
plugins:
  - name: acme.echo
    path: /tmp/acme-wkbench-plugin/bin/acme-echo-plugin
    enabled: true
```

Relative plugin paths are resolved from the project directory containing
`.wkbench`. `enabled` defaults to true when omitted.

Management commands do not all start plugins:

- `wkbench plugin list` prints configured plugins without starting them.
- `wkbench plugin add <name> <path>` creates or updates `.wkbench/plugins.yaml`.
- `wkbench plugin init -dir <dir> -module <module> -name <name>` generates a
  standalone Go plugin module.
- `wkbench plugin doctor` starts enabled configured plugins, performs the
  handshake, and reports manifest/unit status.
- `wkbench plugin inspect <name-or-path>` prints one plugin manifest.

`plugin add`, `plugin init`, and `plugin list` are safe to run even when an
existing configured plugin path is missing. Use `plugin doctor` when you want
to check the executable and manifest.

## Scenario YAML

Reference external units with the plugin name and unit kind:

```yaml
version: wkbench/v2

run:
  id: plugin-echo-demo
  duration: 1s

units:
  echo:
    use: wkbench.demo:demo.echo/v1
    spec:
      message: hello from plugin
```

The host also resolves a bare kind when exactly one loaded plugin provides that
kind and no existing registry kind already uses it. Plugin-qualified references
avoid ambiguity as more plugins are loaded.

Use explicit `inputs` when more than one unit can provide the same port type.
Composition stays in scenario YAML; plugin units should not import other
`units/*` packages.

## Lifecycle

Plugin units receive the same lifecycle calls as local units:

- `Validate` checks only the local spec. It should not call WuKongIM or open
  sockets.
- `Plan` should be deterministic and return the work plan for the unit.
- `Run` may read inputs, emit metrics, set outputs, and write report-friendly
  values.

Inputs and outputs are JSON inline values in Phase 1. Keep large raw samples out
of inline outputs; write compact summaries instead.

## Artifacts

Remote plugin `Run` supports host-managed artifact writes through
`env.OpenArtifact`. Artifacts must be declared in `Definition.Artifacts`; the
host writes them under `run.report_dir/artifacts/<unit>/<artifact-name>` and
records metadata in `report.json`.

Use artifacts for raw samples or larger diagnostic files. Keep report outputs
compact and JSON-friendly.

## Metrics

Remote plugin metrics are supported through aggregate metric flushes. Duration
metric `count`, `sum`, `min`, and `max` are preserved. Exact `p95` and `p99`
latency values are only available for local exact samples, not aggregate-only
remote metrics.

## Phase 1 Limits

- Plugin transport is `wkbench.plugin/v1` over stdio only.
- `Validate`, `Plan`, and `Run` are supported; background lifecycle and richer
  streaming APIs are outside this first plugin path.
- Inputs crossing the process boundary must be wired from non-sensitive inline
  JSON data outputs into non-sensitive inline JSON data inputs. Paged,
  artifact-ref, stream, and local-resource inputs or producer outputs are not
  transported in Phase 1.
- Outputs crossing the process boundary are JSON inline values.
- Large samples belong in artifacts, not inline outputs.
- Official data and control-plane units now run as bundled plugins by default.
  Capability ports, local resources, token-source interfaces, and background
  units remain host-local until the protocol has explicit support for those
  contracts.
