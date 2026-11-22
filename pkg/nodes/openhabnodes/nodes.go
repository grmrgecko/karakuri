package openhabnodes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab"
	"go.uber.org/zap"
)

// connectRetryDelay is how long an armed trigger waits before retrying a
// connection that is missing or not running yet.
const connectRetryDelay = 5 * time.Second

// conn is the driver surface the nodes need; *openhab.Driver satisfies it.
type conn interface {
	Items() openhab.Catalog
	Item(name string) (openhab.Item, bool)
	SubscribeChanges(fn func(openhab.ItemChange)) func()
	SendCommand(ctx context.Context, name, command string) error
}

var _ conn = (*openhab.Driver)(nil)

// resolveConn resolves a node's configured connection to the openHAB driver.
func resolveConn(ec engine.ExecContext, id string) (conn, error) {
	raw, err := ec.Connection(id)
	if err != nil {
		return nil, err
	}
	c, ok := raw.(conn)
	if !ok {
		return nil, fmt.Errorf("connection %s is not an openHAB connection", id)
	}
	return c, nil
}

// connectionField is the openHAB connection picker.
func connectionField() engine.FieldSpec {
	return engine.FieldSpec{
		ID: "connection", Label: "Connection", Type: flow.FieldConnection, Required: true,
		ConnectionKind: openhab.Kind, Help: "openHAB connection to use.",
	}
}

// itemField is an item picker completing against the connection's items.
func itemField(id, label, help string, required bool) engine.FieldSpec {
	return engine.FieldSpec{ID: id, Label: label, Type: flow.FieldString, Control: "openhab-item",
		Required: required, Help: help}
}

// splitNames reads a comma-separated item list.
func splitNames(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// changeMsg renders an item change as a trigger message.
func changeMsg(c openhab.ItemChange, catalog openhab.Catalog, connID string) *engine.Msg {
	place := catalog.Locate(c.Name)
	payload := map[string]any{
		"item":     c.Name,
		"type":     c.Item.Type,
		"label":    c.Item.DisplayLabel(),
		"old":      c.Old,
		"new":      c.New,
		"location": place.LocationLabel,
	}
	meta := map[string]any{"openhab_item": c.Name, "connection_id": connID}
	if c.CaughtUp {
		payload["caught_up"] = true
		meta["caught_up"] = true
	}
	return &engine.Msg{Payload: payload, Topic: c.Name, Meta: meta}
}

// ItemChangedNode starts a run when an item's state changes.
type ItemChangedNode struct{}

func (ItemChangedNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "openhab-item-changed",
		Label:             "openHAB Item Changed",
		Category:          flow.CategoryTrigger,
		Folder:            flow.FolderOpenHAB,
		Description:       "Starts a run when an openHAB item's state changes",
		Icon:              "las la-exchange-alt",
		ConnectionKind:    openhab.Kind,
		TestWithoutParams: true,
		Outputs:           []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortObject}},
		ConfigSpec: []engine.FieldSpec{
			connectionField(),
			itemField("items", "Items", "Items to watch, comma-separated.", false),
			itemField("groups", "Groups", "Watch every member of these groups, at any depth.", false),
			itemField("location", "Location", "Watch every item in this location or anywhere inside it, such as a floor.", false),
			{ID: "item_type", Label: "Only type", Type: flow.FieldString, Placeholder: "Contact",
				Help: "With groups or a location: only items of this type. Number matches dimensioned numbers too."},
			{ID: "from", Label: "From", Type: flow.FieldString, Placeholder: "CLOSED",
				Help: "Only fire when the previous state is this. Empty matches any."},
			{ID: "to", Label: "To", Type: flow.FieldString, Placeholder: "OPEN",
				Help: "Only fire when the new state is this. Empty matches any."},
			{ID: "ignore_caught_up", Label: "Ignore changes caught up after a reconnect", Type: flow.FieldBoolean,
				Help: "After the connection comes back, each item that changed while it was down is reported once " +
					"with its latest state. Turn this on to fire only for changes seen as they happened."},
			{ID: "test_params", Label: "Test parameters", Type: flow.FieldJSON,
				Help: "Payload a test run starts with instead of waiting for a change."},
		},
	}
}

// itemChangedConfig is the decoded openhab-item-changed configuration.
type itemChangedConfig struct {
	Connection     string `json:"connection"`
	Items          string `json:"items"`
	Groups         string `json:"groups"`
	Location       string `json:"location"`
	ItemType       string `json:"item_type"`
	From           string `json:"from"`
	To             string `json:"to"`
	IgnoreCaughtUp bool   `json:"ignore_caught_up"`
}

// Execute passes the seeded message through.
func (ItemChangedNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{Payload: map[string]any{}}
	}
	return engine.One("out", msg), nil
}

// Matches reports whether a change is one the trigger watches. Membership
// is read from the catalog at each change, so an item added to a watched
// group counts from its next change.
func (cfg itemChangedConfig) Matches(c openhab.ItemChange, catalog openhab.Catalog) bool {
	if c.New == "" {
		// An item that disappeared has no state to report.
		return false
	}
	if c.CaughtUp && cfg.IgnoreCaughtUp {
		return false
	}
	watched := false
	for _, name := range splitNames(cfg.Items) {
		if name == c.Name {
			watched = true
		}
	}
	grouped := false
	for _, g := range splitNames(cfg.Groups) {
		if catalog.MemberOf(c.Name, g) {
			grouped = true
		}
	}
	if loc := strings.TrimSpace(cfg.Location); loc != "" && catalog.InLocation(c.Name, loc) {
		grouped = true
	}
	if grouped && strings.TrimSpace(cfg.ItemType) != "" {
		want := strings.TrimSpace(cfg.ItemType)
		it := catalog[c.Name]
		grouped = strings.EqualFold(it.BaseType(), want) || strings.EqualFold(it.Type, want)
	}
	if !watched && !grouped {
		return false
	}
	if cfg.From != "" && c.Old != cfg.From {
		return false
	}
	if cfg.To != "" && c.New != cfg.To {
		return false
	}
	return true
}

// Start watches item changes until the context ends.
func (ItemChangedNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg itemChangedConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	if len(splitNames(cfg.Items)) == 0 && len(splitNames(cfg.Groups)) == 0 && strings.TrimSpace(cfg.Location) == "" {
		return fmt.Errorf("openhab-item-changed needs items, groups, or a location to watch")
	}
	var c conn
	for {
		var err error
		if c, err = resolveConn(ec, cfg.Connection); err == nil {
			break
		}
		ec.Logger().Debug("waiting for openHAB connection", zap.Error(err))
		select {
		case <-ec.Clock().After(connectRetryDelay):
		case <-ctx.Done():
			return nil
		}
	}
	stop := c.SubscribeChanges(func(change openhab.ItemChange) {
		catalog := c.Items()
		if cfg.Matches(change, catalog) {
			emit(changeMsg(change, catalog, cfg.Connection))
		}
	})
	defer stop()
	ec.Logger().Info("openhab-item-changed trigger armed", zap.String("items", cfg.Items),
		zap.String("groups", cfg.Groups), zap.String("location", cfg.Location))
	<-ctx.Done()
	return nil
}

// SendCommandNode sends a command to an item or group.
type SendCommandNode struct{}

func (SendCommandNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:           "openhab-send-command",
		Label:          "openHAB Send Command",
		Folder:         flow.FolderOpenHAB,
		Description:    "Sends a command to an openHAB item or group",
		Icon:           "las la-play-circle",
		ConnectionKind: openhab.Kind,
		Retryable:      true,
		Inputs:         []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs:        []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"}, flow.ErrorPort()},
		ConfigSpec: []engine.FieldSpec{
			connectionField(),
			itemField("item", "Item", "Item or group; a group passes the command to its members.", true),
			{ID: "command", Label: "Command", Type: flow.FieldString, Required: true, Placeholder: "ON",
				Help: "As openHAB takes it: ON, OFF, a percent, h,s,b for a colour, UP, DOWN, STOP, a number, or text. " +
					"{{ }} expressions are interpolated."},
		},
	}
}

// sendCommandConfig is the decoded openhab-send-command configuration.
type sendCommandConfig struct {
	Connection string `json:"connection"`
	Item       string `json:"item"`
	Command    string `json:"command"`
}

// Execute sends the command and passes the message through.
func (SendCommandNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg sendCommandConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}
	item, err := ec.Interpolate(cfg.Item, msg)
	if err != nil {
		return nil, fmt.Errorf("invalid item template: %w", err)
	}
	command, err := ec.Interpolate(cfg.Command, msg)
	if err != nil {
		return nil, fmt.Errorf("invalid command template: %w", err)
	}
	item, command = strings.TrimSpace(item), strings.TrimSpace(command)
	if item == "" || command == "" {
		return nil, fmt.Errorf("openhab-send-command needs an item and a command")
	}
	c, err := resolveConn(ec, cfg.Connection)
	if err != nil {
		return nil, err
	}
	if err := c.SendCommand(ctx, item, command); err != nil {
		return nil, err
	}
	return engine.One("out", msg), nil
}

// GetItemNode reads an item's state when a consumer pulls it.
type GetItemNode struct{}

func (GetItemNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "openhab-get-item",
		Label:             "openHAB Get Item",
		Folder:            flow.FolderOpenHAB,
		Description:       "Reads an openHAB item's current state, pulled when a consumer needs it",
		Icon:              "las la-search",
		ConnectionKind:    openhab.Kind,
		Pure:              true,
		TestWithoutParams: true,
		Outputs:           []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortObject}},
		ConfigSpec: []engine.FieldSpec{
			connectionField(),
			itemField("item", "Item", "Item to read.", true),
		},
	}
}

// getItemConfig is the decoded openhab-get-item configuration.
type getItemConfig struct {
	Connection string `json:"connection"`
	Item       string `json:"item"`
}

// Execute emits the item's state from the connection's item copy, which the
// event stream keeps current.
func (GetItemNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg getItemConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	c, err := resolveConn(ec, cfg.Connection)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(cfg.Item)
	it, ok := c.Item(name)
	if !ok {
		return nil, fmt.Errorf("openHAB has no item %q", name)
	}
	return engine.One("out", &engine.Msg{
		Payload: map[string]any{
			"item":     it.Name,
			"state":    it.State,
			"type":     it.Type,
			"label":    it.DisplayLabel(),
			"location": c.Items().Locate(it.Name).LocationLabel,
		},
		Topic: it.Name,
	}), nil
}
