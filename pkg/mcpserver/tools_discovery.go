package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/cronspec"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addDiscoveryTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "preview_schedule", Description: "Validate a cron expression and preview upcoming execution times using the flow engine's parser. Does not arm a trigger."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Expr     string `json:"expr" jsonschema:"Cron expression, with optional seconds field."`
			Location string `json:"location,omitempty" jsonschema:"IANA timezone; defaults to the server timezone."`
			After    string `json:"after,omitempty" jsonschema:"Optional RFC3339 start time; defaults to now."`
			Count    int    `json:"count,omitempty" jsonschema:"Number of occurrences, 1-20; default 5."`
		}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermFlowsRead); err != nil {
				return nil, nil, err
			}
			if in.Count == 0 {
				in.Count = 5
			}
			if in.Count < 1 || in.Count > 20 {
				return nil, nil, fmt.Errorf("count must be between 1 and 20")
			}
			after := time.Now()
			if in.After != "" {
				var err error
				after, err = time.Parse(time.RFC3339, in.After)
				if err != nil {
					return nil, nil, fmt.Errorf("after must be an RFC3339 timestamp")
				}
			}
			result := map[string]any{"valid": false, "fields": cronspec.FieldLabels(in.Expr), "next": []string{}}
			if err := cronspec.Validate(in.Expr, in.Location); err != nil {
				result["error"] = err.Error()
				return nil, result, nil
			}
			next, err := cronspec.Next(in.Expr, in.Location, after, in.Count)
			if err != nil {
				result["error"] = err.Error()
				return nil, result, nil
			}
			times := make([]string, 0, len(next))
			for _, at := range next {
				times = append(times, at.Format(time.RFC3339))
			}
			result["valid"] = true
			result["description"] = cronspec.Describe(in.Expr)
			result["next"] = times
			return nil, result, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "list_ha_services", Description: "Discover Home Assistant service fields, selectors, and targets before configuring an action. Reads the running connection; never invokes a service."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			ConnectionID string `json:"connection_id" jsonschema:"Home Assistant connection UUID."`
			Domain       string `json:"domain,omitempty" jsonschema:"Exact domain filter, for example light."`
			Service      string `json:"service,omitempty" jsonschema:"Exact service name filter, for example turn_on."`
			Offset       int    `json:"offset,omitempty"`
			Limit        int    `json:"limit,omitempty" jsonschema:"Page size, 1-100; default 50."`
		}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
				return nil, nil, err
			}
			if in.Limit == 0 {
				in.Limit = 50
			}
			if in.Offset < 0 || in.Limit < 1 || in.Limit > 100 {
				return nil, nil, fmt.Errorf("offset must be nonnegative and limit between 1 and 100")
			}
			driver, err := connops.HADriver(in.ConnectionID)
			if err != nil {
				return nil, nil, opErr(err)
			}
			services, err := driver.GetServices(ctx)
			if err != nil {
				return nil, nil, err
			}
			keys := []string{}
			for domain, items := range services {
				if in.Domain != "" && domain != in.Domain {
					continue
				}
				for name := range items {
					if in.Service == "" || name == in.Service {
						keys = append(keys, domain+"."+name)
					}
				}
			}
			sort.Strings(keys)
			start := min(in.Offset, len(keys))
			end := start + min(in.Limit, len(keys)-start)
			items := []map[string]any{}
			for _, key := range keys[start:end] {
				domain, name, _ := strings.Cut(key, ".")
				items = append(items, map[string]any{"domain": domain, "service": name, "schema": services[domain][name]})
			}
			return nil, map[string]any{"services": items, "total": len(keys), "has_more": end < len(keys), "next_offset": end}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "capture_mqtt_messages", Description: "Capture real MQTT payload samples for a bounded time/count without publishing. Shares the connection and removes only this capture's subscription when done. Retained messages may be included."}, captureMQTTMessages)
}

type mqttCaptureInput struct {
	ConnectionID    string `json:"connection_id"`
	TopicFilter     string `json:"topic_filter" jsonschema:"MQTT topic filter; + and # wildcards are allowed. Shared subscriptions are not supported."`
	DurationSeconds int    `json:"duration_seconds,omitempty" jsonschema:"Capture duration including subscription setup, 1-30; default 5."`
	MaxMessages     int    `json:"max_messages,omitempty" jsonschema:"Message limit, 1-100; default 20."`
}

type capturedMQTTMessage struct {
	Topic      string    `json:"topic"`
	Payload    string    `json:"payload"`
	Encoding   string    `json:"encoding"`
	ReceivedAt time.Time `json:"received_at"`
}

func captureMQTTMessages(ctx context.Context, req *mcp.CallToolRequest, in mqttCaptureInput) (*mcp.CallToolResult, any, error) {
	if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
		return nil, nil, err
	}
	if in.DurationSeconds == 0 {
		in.DurationSeconds = 5
	}
	if in.MaxMessages == 0 {
		in.MaxMessages = 20
	}
	if in.DurationSeconds < 1 || in.DurationSeconds > 30 || in.MaxMessages < 1 || in.MaxMessages > 100 {
		return nil, nil, fmt.Errorf("duration_seconds must be 1-30 and max_messages 1-100")
	}
	if in.TopicFilter == "" || len(in.TopicFilter) > 65535 || !utf8.ValidString(in.TopicFilter) || strings.ContainsRune(in.TopicFilter, 0) || strings.HasPrefix(in.TopicFilter, "$share/") {
		return nil, nil, fmt.Errorf("invalid topic filter")
	}
	levels := strings.Split(in.TopicFilter, "/")
	for i, level := range levels {
		if strings.ContainsAny(level, "+#") && !(level == "+" || level == "#" && i == len(levels)-1) {
			return nil, nil, fmt.Errorf("wildcards must occupy a whole level and # must be last")
		}
	}
	driver, err := connops.MQTTDriver(in.ConnectionID)
	if err != nil {
		return nil, nil, opErr(err)
	}
	if driver.Status().State != integrations.StateConnected {
		return nil, nil, fmt.Errorf("MQTT connection is not connected")
	}
	captureCtx, cancel := context.WithTimeout(ctx, time.Duration(in.DurationSeconds)*time.Second)
	defer cancel()
	var mu sync.Mutex
	messages := []capturedMQTTMessage{}
	size := 0
	reason := ""
	done := make(chan struct{})
	finish := func(why string) {
		if reason == "" {
			reason = why
			close(done)
		}
	}
	unsubscribe, err := driver.Subscribe(in.TopicFilter, 0, func(topic string, payload []byte) {
		mu.Lock()
		defer mu.Unlock()
		if reason != "" {
			return
		}
		// Bound encoded output before copying payloads off the driver's dispatch loop.
		if len(payload) > 128<<10 {
			finish("byte_limit")
			return
		}
		msg := capturedMQTTMessage{Topic: topic, ReceivedAt: time.Now(), Encoding: "utf8"}
		if utf8.Valid(payload) {
			msg.Payload = string(payload)
		} else {
			msg.Encoding = "base64"
			msg.Payload = base64.StdEncoding.EncodeToString(payload)
		}
		encoded, _ := json.Marshal(msg)
		if size+len(encoded) > 1<<20 {
			finish("byte_limit")
			return
		}
		size += len(encoded)
		messages = append(messages, msg)
		if len(messages) >= in.MaxMessages {
			finish("message_limit")
		}
	})
	if err != nil {
		return nil, nil, err
	}
	defer unsubscribe()
	select {
	case <-done:
	case <-captureCtx.Done():
	}
	mu.Lock()
	defer mu.Unlock()
	if ctx.Err() != nil {
		finish("canceled")
		return nil, nil, ctx.Err()
	}
	if reason == "" {
		finish("timeout")
	}
	return nil, map[string]any{"messages": messages, "stop_reason": reason, "truncated": reason == "byte_limit", "subscription_active": driver.Subscribed(in.TopicFilter), "connection_state": driver.Status().State}, nil
}
