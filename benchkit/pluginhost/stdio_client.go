package pluginhost

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/protocol"
)

type StdioClient struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	reader *protocol.FrameReader
	writer *protocol.FrameWriter

	ioMu      sync.Mutex
	closeOnce sync.Once
	closeErr  error
}

func StartStdioClient(ctx context.Context, path string) (*StdioClient, error) {
	cmd := exec.CommandContext(ctx, path)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open plugin stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open plugin stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start plugin: %w", err)
	}

	client := &StdioClient{
		cmd:    cmd,
		stdin:  stdin,
		reader: protocol.NewFrameReader(stdout, 16<<20),
		writer: protocol.NewFrameWriter(stdin),
	}
	return client, nil
}

func (c *StdioClient) Handshake(ctx context.Context) (Plugin, error) {
	_ = ctx
	const requestID = "handshake"
	c.ioMu.Lock()
	defer c.ioMu.Unlock()

	if err := c.writer.WriteFrame(&protocol.Frame{
		RequestId: requestID,
		Body: &protocol.Frame_HandshakeRequest{HandshakeRequest: &protocol.HandshakeRequest{
			HostProtocol: "wkbench.plugin/v1",
			MinProtocol:  "wkbench.plugin/v1",
			MaxProtocol:  "wkbench.plugin/v1",
		}},
	}); err != nil {
		return Plugin{}, fmt.Errorf("write handshake request: %w", err)
	}

	frame, err := c.reader.ReadFrame()
	if err != nil {
		return Plugin{}, fmt.Errorf("read handshake response: %w", err)
	}
	if frame.GetRequestId() != requestID {
		return Plugin{}, fmt.Errorf("handshake response request id = %q, want %q", frame.GetRequestId(), requestID)
	}
	if rpcErr := frame.GetError(); rpcErr != nil {
		return Plugin{}, fmt.Errorf("plugin error %s: %s", rpcErr.GetCode(), rpcErr.GetMessage())
	}
	response := frame.GetHandshakeResponse()
	if response == nil {
		return Plugin{}, fmt.Errorf("expected handshake response frame")
	}
	manifest := pluginFromProto(response.GetManifest())
	if response.GetSelectedProtocol() != "" {
		manifest.Protocol = response.GetSelectedProtocol()
	}
	return manifest, nil
}

func (c *StdioClient) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.close()
	})
	return c.closeErr
}

func (c *StdioClient) close() error {
	_ = c.stdin.Close()
	c.ioMu.Lock()
	defer c.ioMu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- c.cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("wait plugin: %w", err)
		}
		return nil
	case <-time.After(2 * time.Second):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		err := <-done
		if err != nil {
			return fmt.Errorf("plugin did not exit after stdin close: %w", err)
		}
		return fmt.Errorf("plugin did not exit after stdin close")
	}
}

func pluginFromProto(manifest *protocol.PluginManifest) Plugin {
	if manifest == nil {
		return Plugin{}
	}
	return Plugin{
		Name:     manifest.GetName(),
		Version:  manifest.GetVersion(),
		Protocol: manifest.GetProtocol(),
		Source:   manifest.GetSource(),
		Checksum: manifest.GetChecksum(),
		Units:    unitsFromProto(manifest.GetUnits(), manifest.GetName()),
	}
}

func unitsFromProto(units []*protocol.UnitDefinition, pluginName string) []Unit {
	out := make([]Unit, 0, len(units))
	for _, unit := range units {
		out = append(out, unitFromProto(unit, pluginName))
	}
	return out
}

func unitFromProto(unit *protocol.UnitDefinition, pluginName string) Unit {
	if unit == nil {
		return Unit{PluginName: pluginName}
	}
	return Unit{
		PluginName:  pluginName,
		Kind:        unit.GetKind(),
		Title:       unit.GetTitle(),
		Description: unit.GetDescription(),
		Inputs:      portsFromProto(unit.GetInputs()),
		Outputs:     portsFromProto(unit.GetOutputs()),
		Metrics:     metricsFromProto(unit.GetMetrics()),
		Artifacts:   artifactsFromProto(unit.GetArtifacts()),
	}
}

func portsFromProto(ports []*protocol.PortDef) []contract.PortDef {
	out := make([]contract.PortDef, 0, len(ports))
	for _, port := range ports {
		out = append(out, portFromProto(port))
	}
	return out
}

func portFromProto(port *protocol.PortDef) contract.PortDef {
	if port == nil {
		return contract.PortDef{}
	}
	meta := port.GetMeta()
	return contract.PortDef{
		Name:     port.GetName(),
		Type:     contract.PortType(port.GetType()),
		Optional: port.GetOptional(),
		Meta: contract.PortMeta{
			Boundary:        contract.PortBoundary(meta.GetBoundary()),
			Transport:       contract.PortTransport(meta.GetTransport()),
			Schema:          meta.GetSchema(),
			Encodings:       slices.Clone(meta.GetEncodings()),
			MaxPayloadBytes: meta.GetMaxPayloadBytes(),
			Sensitive:       meta.GetSensitive(),
			Reportable:      meta.GetReportable(),
			Operations:      slices.Clone(meta.GetOperations()),
		},
	}
}

func metricsFromProto(metrics []*protocol.MetricDef) []contract.MetricDef {
	out := make([]contract.MetricDef, 0, len(metrics))
	for _, metric := range metrics {
		if metric == nil {
			out = append(out, contract.MetricDef{})
			continue
		}
		out = append(out, contract.MetricDef{Name: metric.GetName(), Type: metric.GetType()})
	}
	return out
}

func artifactsFromProto(artifacts []*protocol.ArtifactDef) []contract.ArtifactDef {
	out := make([]contract.ArtifactDef, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil {
			out = append(out, contract.ArtifactDef{})
			continue
		}
		out = append(out, contract.ArtifactDef{Name: artifact.GetName(), ContentType: artifact.GetContentType()})
	}
	return out
}
