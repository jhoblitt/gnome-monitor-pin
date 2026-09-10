package displayconfig

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

const (
	busName                    = "org.gnome.Mutter.DisplayConfig"
	objectPath dbus.ObjectPath = "/org/gnome/Mutter/DisplayConfig"
	iface                      = "org.gnome.Mutter.DisplayConfig"

	monitorsChanged  = iface + ".MonitorsChanged"
	nameOwnerChanged = "org.freedesktop.DBus.NameOwnerChanged"

	// callTimeout bounds every bus round trip: a compositor wedged
	// mid-modeset must surface as an error, not stall the watch loop.
	callTimeout = 30 * time.Second
)

// ApplyMonitorsConfig's method argument.
const (
	methodVerify    uint32 = 0
	methodTemporary uint32 = 1
)

// Client is a connection to mutter's display configuration.
type Client struct {
	conn *dbus.Conn
	obj  dbus.BusObject
}

// Connect opens the session bus.
func Connect(ctx context.Context) (*Client, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connecting to the session bus: %w", err)
	}
	if ctx.Err() != nil {
		return nil, errors.Join(ctx.Err(), conn.Close())
	}
	return &Client{conn: conn, obj: conn.Object(busName, objectPath)}, nil
}

// Close closes the bus connection; a channel from Subscribe closes with it.
func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) call(ctx context.Context, method string, args ...any) *dbus.Call {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return c.obj.CallWithContext(ctx, iface+"."+method, dbus.FlagNoAutoStart, args...)
}

// CurrentState reads what mutter shows now.
func (c *Client) CurrentState(ctx context.Context) (layout.State, error) {
	var (
		serial   uint32
		monitors []monitor
		logicals []logical
		props    map[string]dbus.Variant
	)
	if err := c.call(ctx, "GetCurrentState").Store(&serial, &monitors, &logicals, &props); err != nil {
		return layout.State{}, fmt.Errorf("reading state from %s: %w", busName, err)
	}
	return decodeState(serial, monitors, logicals, props), nil
}

// Verify asks mutter whether t is applicable without applying it.
func (c *Client) Verify(ctx context.Context, t layout.Target) error {
	if err := c.apply(ctx, t, methodVerify); err != nil {
		return fmt.Errorf("verifying with %s: %w", busName, err)
	}
	return nil
}

// Apply makes t the current configuration without persisting it to
// mutter's monitors.xml.
func (c *Client) Apply(ctx context.Context, t layout.Target) error {
	if err := c.apply(ctx, t, methodTemporary); err != nil {
		return fmt.Errorf("applying with %s: %w", busName, err)
	}
	return nil
}

func (c *Client) apply(ctx context.Context, t layout.Target, method uint32) error {
	return c.call(ctx, "ApplyMonitorsConfig", t.Serial, method, encodeTarget(t), encodeProperties(t)).Err
}

// Subscribe delivers a value after each MonitorsChanged signal from mutter
// and after each change of owner of mutter's bus name, coalescing a burst
// into one, and closes the channel when ctx ends or the bus connection
// closes.
func (c *Client) Subscribe(ctx context.Context) (<-chan struct{}, error) {
	signals := make(chan *dbus.Signal, 16)
	c.conn.Signal(signals)
	matches := [][]dbus.MatchOption{
		{
			dbus.WithMatchSender(busName),
			dbus.WithMatchObjectPath(objectPath),
			dbus.WithMatchInterface(iface),
			dbus.WithMatchMember("MonitorsChanged"),
		},
		{
			dbus.WithMatchSender("org.freedesktop.DBus"),
			dbus.WithMatchInterface("org.freedesktop.DBus"),
			dbus.WithMatchMember("NameOwnerChanged"),
			dbus.WithMatchArg(0, busName),
		},
	}
	for _, m := range matches {
		if err := c.addMatch(ctx, m); err != nil {
			c.conn.RemoveSignal(signals)
			return nil, fmt.Errorf("subscribing to display signals: %w", err)
		}
	}
	events := make(chan struct{}, 1)
	go func() {
		defer close(events)
		defer c.conn.RemoveSignal(signals)
		for {
			select {
			case <-ctx.Done():
				return
			case sig, ok := <-signals:
				if !ok {
					return
				}
				if !relevant(sig) {
					continue
				}
				select {
				case events <- struct{}{}:
				default:
				}
			}
		}
	}()
	return events, nil
}

func (c *Client) addMatch(ctx context.Context, options []dbus.MatchOption) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return c.conn.AddMatchSignalContext(ctx, options...)
}

// relevant accepts mutter's MonitorsChanged, and a NameOwnerChanged that
// announces a new owner of mutter's name; a name going away is not an
// event, the next owner's arrival is.
func relevant(sig *dbus.Signal) bool {
	switch sig.Name {
	case monitorsChanged:
		return true
	case nameOwnerChanged:
		if len(sig.Body) < 3 {
			return false
		}
		owner, ok := sig.Body[2].(string)
		return ok && owner != ""
	default:
		return false
	}
}
