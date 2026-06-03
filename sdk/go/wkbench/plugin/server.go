package plugin

import (
	"errors"
	"io"
	"slices"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/pluginhost"
	"github.com/WuKongIM/wkbench/benchkit/protocol"
)

type Plugin struct {
	Name    string
	Version string
	Units   []contract.Unit
}

func Serve(plugin Plugin, stdin io.Reader, stdout io.Writer) error {
	manifest := ManifestFromUnits(plugin.Name, plugin.Version, plugin.Units)
	reader := protocol.NewFrameReader(stdin, 16<<20)
	writer := protocol.NewFrameWriter(stdout)

	for {
		frame, err := reader.ReadFrame()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		switch frame.Body.(type) {
		case *protocol.Frame_HandshakeRequest:
			if err := writer.WriteFrame(&protocol.Frame{
				RequestId: frame.GetRequestId(),
				Body: &protocol.Frame_HandshakeResponse{HandshakeResponse: &protocol.HandshakeResponse{
					Manifest:         manifestToProto(manifest),
					SelectedProtocol: "wkbench.plugin/v1",
				}},
			}); err != nil {
				return err
			}
		case *protocol.Frame_ListUnitsRequest:
			if err := writer.WriteFrame(&protocol.Frame{
				RequestId: frame.GetRequestId(),
				Body: &protocol.Frame_ListUnitsResponse{ListUnitsResponse: &protocol.ListUnitsResponse{
					Units: unitsToProto(manifest.Units),
				}},
			}); err != nil {
				return err
			}
		default:
			if err := writer.WriteFrame(&protocol.Frame{
				RequestId: frame.GetRequestId(),
				Body: &protocol.Frame_Error{Error: &protocol.Error{
					Code:    "UNSUPPORTED",
					Message: "unsupported frame",
				}},
			}); err != nil {
				return err
			}
		}
	}
}

func ManifestFromUnits(name, version string, units []contract.Unit) pluginhost.Plugin {
	out := pluginhost.Plugin{Name: name, Version: version, Protocol: "wkbench.plugin/v1"}
	for _, unit := range units {
		def := unit.Definition()
		out.Units = append(out.Units, pluginhost.Unit{
			Kind:        def.Kind,
			Title:       def.Title,
			Description: def.Description,
			Inputs:      clonePortDefs(def.Inputs),
			Outputs:     clonePortDefs(def.Outputs),
			Metrics:     slices.Clone(def.Metrics),
			Artifacts:   slices.Clone(def.Artifacts),
		})
	}
	return out
}

func clonePortDefs(ports []contract.PortDef) []contract.PortDef {
	ports = slices.Clone(ports)
	for i := range ports {
		ports[i].Meta.Encodings = slices.Clone(ports[i].Meta.Encodings)
		ports[i].Meta.Operations = slices.Clone(ports[i].Meta.Operations)
	}
	return ports
}

func manifestToProto(manifest pluginhost.Plugin) *protocol.PluginManifest {
	return &protocol.PluginManifest{
		Name:     manifest.Name,
		Version:  manifest.Version,
		Protocol: manifest.Protocol,
		Source:   manifest.Source,
		Checksum: manifest.Checksum,
		Units:    unitsToProto(manifest.Units),
	}
}

func unitsToProto(units []pluginhost.Unit) []*protocol.UnitDefinition {
	out := make([]*protocol.UnitDefinition, 0, len(units))
	for _, unit := range units {
		out = append(out, unitToProto(unit))
	}
	return out
}

func unitToProto(unit pluginhost.Unit) *protocol.UnitDefinition {
	return &protocol.UnitDefinition{
		Kind:        unit.Kind,
		Title:       unit.Title,
		Description: unit.Description,
		Inputs:      portsToProto(unit.Inputs),
		Outputs:     portsToProto(unit.Outputs),
		Metrics:     metricsToProto(unit.Metrics),
		Artifacts:   artifactsToProto(unit.Artifacts),
	}
}

func portsToProto(ports []contract.PortDef) []*protocol.PortDef {
	out := make([]*protocol.PortDef, 0, len(ports))
	for _, port := range ports {
		out = append(out, portToProto(port))
	}
	return out
}

func portToProto(port contract.PortDef) *protocol.PortDef {
	return &protocol.PortDef{
		Name:     port.Name,
		Type:     string(port.Type),
		Optional: port.Optional,
		Meta: &protocol.PortMeta{
			Boundary:        string(port.Meta.Boundary),
			Transport:       string(port.Meta.Transport),
			Schema:          port.Meta.Schema,
			Encodings:       slices.Clone(port.Meta.Encodings),
			MaxPayloadBytes: port.Meta.MaxPayloadBytes,
			Sensitive:       port.Meta.Sensitive,
			Reportable:      port.Meta.Reportable,
			Operations:      slices.Clone(port.Meta.Operations),
		},
	}
}

func metricsToProto(metrics []contract.MetricDef) []*protocol.MetricDef {
	out := make([]*protocol.MetricDef, 0, len(metrics))
	for _, metric := range metrics {
		out = append(out, &protocol.MetricDef{Name: metric.Name, Type: metric.Type})
	}
	return out
}

func artifactsToProto(artifacts []contract.ArtifactDef) []*protocol.ArtifactDef {
	out := make([]*protocol.ArtifactDef, 0, len(artifacts))
	for _, artifact := range artifacts {
		out = append(out, &protocol.ArtifactDef{Name: artifact.Name, ContentType: artifact.ContentType})
	}
	return out
}
