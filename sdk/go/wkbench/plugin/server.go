package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/pluginhost"
	"github.com/WuKongIM/wkbench/benchkit/protocol"
)

type Plugin struct {
	Name    string
	Version string
	Units   []contract.Unit
}

type server struct {
	manifest    pluginhost.Plugin
	unitsByKind map[string]contract.Unit
}

func newServer(plugin Plugin) *server {
	unitsByKind := make(map[string]contract.Unit, len(plugin.Units))
	for _, unit := range plugin.Units {
		unitsByKind[unit.Definition().Kind] = unit
	}
	return &server{
		manifest:    ManifestFromUnits(plugin.Name, plugin.Version, plugin.Units),
		unitsByKind: unitsByKind,
	}
}

func (s *server) unit(kind string) (contract.Unit, error) {
	unit, ok := s.unitsByKind[kind]
	if !ok {
		return nil, fmt.Errorf("unit kind %q is not registered", kind)
	}
	return unit, nil
}

func Serve(plugin Plugin, stdin io.Reader, stdout io.Writer) error {
	srv := newServer(plugin)
	reader := protocol.NewFrameReader(stdin, 16<<20)
	writer := protocol.NewFrameWriter(stdout)
	ctx := context.Background()

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
					Manifest:         manifestToProto(srv.manifest),
					SelectedProtocol: "wkbench.plugin/v1",
				}},
			}); err != nil {
				return err
			}
		case *protocol.Frame_ListUnitsRequest:
			if err := writer.WriteFrame(&protocol.Frame{
				RequestId: frame.GetRequestId(),
				Body: &protocol.Frame_ListUnitsResponse{ListUnitsResponse: &protocol.ListUnitsResponse{
					Units: unitsToProto(srv.manifest.Units),
				}},
			}); err != nil {
				return err
			}
		case *protocol.Frame_ValidateRequest:
			if err := srv.handleValidate(ctx, frame, frame.GetValidateRequest(), writer); err != nil {
				return err
			}
		case *protocol.Frame_PlanRequest:
			if err := srv.handlePlan(ctx, frame, frame.GetPlanRequest(), writer); err != nil {
				return err
			}
		case *protocol.Frame_RunRequest:
			if err := srv.handleRun(ctx, frame, frame.GetRunRequest(), writer); err != nil {
				return err
			}
		default:
			if err := writeProtocolError(writer, frame.GetRequestId(), "UNSUPPORTED", "unsupported frame"); err != nil {
				return err
			}
		}
	}
}

func (s *server) handleValidate(ctx context.Context, frame *protocol.Frame, req *protocol.ValidateRequest, writer *protocol.FrameWriter) error {
	unit, err := s.unit(req.GetKind())
	if err != nil {
		return writeProtocolError(writer, frame.GetRequestId(), "CONFIG_ERROR", err.Error())
	}
	spec, err := decodeSpecMap(req.GetSpecJson())
	if err != nil {
		return writeProtocolError(writer, frame.GetRequestId(), "CONFIG_ERROR", err.Error())
	}
	env := contract.NewTestRunEnv("", req.GetUnitName(), nil, spec)
	if err := unit.Validate(ctx, env); err != nil {
		return writeProtocolError(writer, frame.GetRequestId(), "CONFIG_ERROR", err.Error())
	}
	return writer.WriteFrame(&protocol.Frame{
		RequestId: frame.GetRequestId(),
		Body:      &protocol.Frame_ValidateResponse{ValidateResponse: &protocol.ValidateResponse{}},
	})
}

func (s *server) handlePlan(ctx context.Context, frame *protocol.Frame, req *protocol.PlanRequest, writer *protocol.FrameWriter) error {
	unit, err := s.unit(req.GetKind())
	if err != nil {
		return writeProtocolError(writer, frame.GetRequestId(), "PLAN_ERROR", err.Error())
	}
	spec, err := decodeSpecMap(req.GetSpecJson())
	if err != nil {
		return writeProtocolError(writer, frame.GetRequestId(), "PLAN_ERROR", err.Error())
	}
	env := contract.NewTestRunEnv(req.GetRunId(), req.GetUnitName(), nil, spec)
	env.SetRunDuration(time.Duration(req.GetRunDurationMillis()) * time.Millisecond)
	plan, err := unit.Plan(ctx, env)
	if err != nil {
		return writeProtocolError(writer, frame.GetRequestId(), "PLAN_ERROR", err.Error())
	}
	payload, err := encodeJSONPayload(plan)
	if err != nil {
		return writeProtocolError(writer, frame.GetRequestId(), "PLAN_ERROR", err.Error())
	}
	return writer.WriteFrame(&protocol.Frame{
		RequestId: frame.GetRequestId(),
		Body:      &protocol.Frame_PlanResponse{PlanResponse: &protocol.PlanResponse{PlanJson: payload}},
	})
}

func (s *server) handleRun(ctx context.Context, frame *protocol.Frame, req *protocol.RunRequest, writer *protocol.FrameWriter) error {
	unit, err := s.unit(req.GetKind())
	if err != nil {
		return writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
	}
	spec, err := decodeSpecMap(req.GetSpecJson())
	if err != nil {
		return writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
	}
	inputs := make(map[string]any, len(req.GetInputs()))
	for name, value := range req.GetInputs() {
		var decoded any
		if value != nil && len(value.GetPayload()) > 0 {
			if err := json.Unmarshal(value.GetPayload(), &decoded); err != nil {
				return writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", fmt.Sprintf("decode input %q json: %v", name, err))
			}
		}
		inputs[name] = decoded
	}
	env := contract.NewTestRunEnv(req.GetRunId(), req.GetUnitName(), inputs, spec)
	env.SetRunDuration(time.Duration(req.GetRunDurationMillis()) * time.Millisecond)
	if err := unit.Run(ctx, env); err != nil {
		return writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
	}
	for _, output := range unit.Definition().Outputs {
		value, ok := env.Output(output.Name)
		if !ok {
			continue
		}
		payload, err := encodeJSONPayload(value)
		if err != nil {
			return writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
		}
		if err := writer.WriteFrame(&protocol.Frame{
			RequestId: frame.GetRequestId(),
			Body: &protocol.Frame_SetOutput{SetOutput: &protocol.SetOutput{
				Name: output.Name,
				Value: &protocol.PortValue{
					Type:       string(output.Type),
					Encoding:   "json",
					Transport:  string(output.Meta.Transport),
					Sensitive:  output.Meta.Sensitive,
					Reportable: output.Meta.Reportable,
					Payload:    payload,
				},
			}},
		}); err != nil {
			return err
		}
	}
	return writer.WriteFrame(&protocol.Frame{
		RequestId: frame.GetRequestId(),
		Body:      &protocol.Frame_TerminalStatus{TerminalStatus: &protocol.TerminalStatus{Ok: true}},
	})
}

func decodeSpecMap(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("decode spec json: %w", err)
	}
	return spec, nil
}

func encodeJSONPayload(value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode json payload: %w", err)
	}
	return payload, nil
}

func writeProtocolError(writer *protocol.FrameWriter, requestID, code, message string) error {
	return writer.WriteFrame(&protocol.Frame{
		RequestId: requestID,
		Body: &protocol.Frame_Error{Error: &protocol.Error{
			Code:    code,
			Message: message,
		}},
	})
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
