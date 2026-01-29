package plugin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"squad/aitools"
	pb "squad/plugin/proto"
)

// Handshake is the handshake config for plugins
var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "SQUAD_PLUGIN",
	MagicCookieValue: "squad-tool-plugin-v1",
}

// ToolInfo contains metadata about a tool
type ToolInfo struct {
	Name        string
	Description string
	Schema      aitools.Schema
}

// ToolProvider is the interface that all tool plugins must implement
type ToolProvider interface {
	// Configure passes settings from HCL config to the plugin
	Configure(settings map[string]string) error

	// Call invokes a tool with the given JSON payload
	Call(toolName string, payload string) (string, error)

	// GetToolInfo returns metadata about a specific tool
	GetToolInfo(toolName string) (*ToolInfo, error)

	// ListTools returns info for all tools this plugin provides
	ListTools() ([]*ToolInfo, error)
}

// ToolPluginGRPCPlugin is the plugin.GRPCPlugin implementation
type ToolPluginGRPCPlugin struct {
	plugin.Plugin
	Impl ToolProvider
}

func (p *ToolPluginGRPCPlugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	pb.RegisterToolPluginServer(s, &GRPCServer{Impl: p.Impl})
	return nil
}

func (p *ToolPluginGRPCPlugin) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &GRPCClient{client: pb.NewToolPluginClient(c)}, nil
}

// GRPCClient is the gRPC client implementation of ToolProvider
type GRPCClient struct {
	client pb.ToolPluginClient
}

func (c *GRPCClient) Configure(settings map[string]string) error {
	resp, err := c.client.Configure(context.Background(), &pb.ConfigureRequest{
		Settings: settings,
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("configure failed: %s", resp.Error)
	}
	return nil
}

func (c *GRPCClient) Call(toolName string, payload string) (string, error) {
	resp, err := c.client.Call(context.Background(), &pb.CallRequest{
		ToolName: toolName,
		Payload:  payload,
	})
	if err != nil {
		return "", err
	}
	return resp.Result, nil
}

func (c *GRPCClient) GetToolInfo(toolName string) (*ToolInfo, error) {
	resp, err := c.client.GetToolInfo(context.Background(), &pb.GetToolInfoRequest{
		ToolName: toolName,
	})
	if err != nil {
		return nil, err
	}
	return protoToToolInfo(resp.Tool)
}

func (c *GRPCClient) ListTools() ([]*ToolInfo, error) {
	resp, err := c.client.ListTools(context.Background(), &pb.ListToolsRequest{})
	if err != nil {
		return nil, err
	}
	tools := make([]*ToolInfo, 0, len(resp.Tools))
	for _, t := range resp.Tools {
		info, err := protoToToolInfo(t)
		if err != nil {
			return nil, err
		}
		tools = append(tools, info)
	}
	return tools, nil
}

// protoToToolInfo converts a protobuf ToolInfo to our ToolInfo
func protoToToolInfo(t *pb.ToolInfo) (*ToolInfo, error) {
	var schema aitools.Schema
	if t.SchemaJson != "" {
		if err := json.Unmarshal([]byte(t.SchemaJson), &schema); err != nil {
			return nil, err
		}
	}
	return &ToolInfo{
		Name:        t.Name,
		Description: t.Description,
		Schema:      schema,
	}, nil
}

// GRPCServer is the gRPC server implementation that wraps a ToolProvider
type GRPCServer struct {
	pb.UnimplementedToolPluginServer
	Impl ToolProvider
}

func (s *GRPCServer) Configure(ctx context.Context, req *pb.ConfigureRequest) (*pb.ConfigureResponse, error) {
	err := s.Impl.Configure(req.Settings)
	if err != nil {
		return &pb.ConfigureResponse{Success: false, Error: err.Error()}, nil
	}
	return &pb.ConfigureResponse{Success: true}, nil
}

func (s *GRPCServer) Call(ctx context.Context, req *pb.CallRequest) (*pb.CallResponse, error) {
	result, err := s.Impl.Call(req.ToolName, req.Payload)
	if err != nil {
		return nil, err
	}
	return &pb.CallResponse{Result: result}, nil
}

func (s *GRPCServer) GetToolInfo(ctx context.Context, req *pb.GetToolInfoRequest) (*pb.GetToolInfoResponse, error) {
	info, err := s.Impl.GetToolInfo(req.ToolName)
	if err != nil {
		return nil, err
	}
	return &pb.GetToolInfoResponse{
		Tool: toolInfoToProto(info),
	}, nil
}

func (s *GRPCServer) ListTools(ctx context.Context, req *pb.ListToolsRequest) (*pb.ListToolsResponse, error) {
	tools, err := s.Impl.ListTools()
	if err != nil {
		return nil, err
	}
	protoTools := make([]*pb.ToolInfo, 0, len(tools))
	for _, t := range tools {
		protoTools = append(protoTools, toolInfoToProto(t))
	}
	return &pb.ListToolsResponse{Tools: protoTools}, nil
}

// toolInfoToProto converts our ToolInfo to protobuf ToolInfo
func toolInfoToProto(t *ToolInfo) *pb.ToolInfo {
	schemaJSON, _ := json.Marshal(t.Schema)
	return &pb.ToolInfo{
		Name:        t.Name,
		Description: t.Description,
		SchemaJson:  string(schemaJSON),
	}
}

// PluginMap is the map of plugins we can dispense
var PluginMap = map[string]plugin.Plugin{
	"tool": &ToolPluginGRPCPlugin{},
}
