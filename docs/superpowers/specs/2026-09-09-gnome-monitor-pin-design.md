# gnome-monitor-pin design

Date: 2026-09-09. Status: approved in chat, revised after two adversarial
review rounds, implementation pending.

## Problem

GNOME (mutter 49, Wayland) collapses a six-monitor 2x3 grid into a linear
1x6 row every time one monitor is powered off or on. The six monitors hang
off two discrete AMD GPUs through DisplayPort MST; each Dell U2413 is its
own MST branch device, so powering a panel off destroys its DRM connector,
and for a chain head every downstream connector too.

Two separate mutter behaviors produce the collapse:

- **Power off.** Mutter looks up a stored configuration for the exact set
  of connected monitors. No five-monitor set was ever stored, and
  `meta_monitor_manager_ensure_configured` has no "keep the survivors where
  they are" strategy: it tries the stored configuration, a suggested
  layout, the previous in-memory configuration, and then generates the
  linear row. This half happens even with stable connector names.
- **Power on.** The kernel recreates the connector under the lowest free
  `DP-N` name, which is often a new one. Mutter identifies a monitor by
  connector, vendor, product, and serial together (`meta_monitor_spec_equals`
  in `src/backends/meta-monitor.c` compares the connector first), the store
  is a plain hash lookup keyed on the whole set of monitor identities, and
  the previous in-memory configuration is only reused when its key equals
  the current set. So the six-monitor grid the user saved does not match,
  and mutter generates the linear row again. `~/.config/monitors.xml` on
  this machine holds 29 saved grids that differ only in connector names.

Pre-generating configurations is not viable: a saved configuration names
all six connectors at once, the assignment space is combinatorial (one
monitor has appeared under ten names), and mutter reads the file once at
startup and rewrites it from memory on every save from Settings. The only
stable identity a monitor has across re-enumeration is its EDID vendor,
product, and serial.

Upstream knows: mutter issue #4036 "Make monitors configurations connector
agnostic" and merge request !984 "Ignore monitor connector ID when
possible" (open since 2019, needs rebase) address the power-on half; issue
#549 reported the rename half on a dock in 2019. Nothing found upstream
addresses the power-off half. !984 is the upstream fix to watch; it is not
a parallel track for this project.

Mutter keeps a three-deep history of applied configurations and, on a
hardware re-read that leaves the monitor set unchanged, re-applies the
most recent one whose set still matches. Once this tool has applied a
layout, the head of that history is the linear row the tool replaced, so a
resume from suspend or a property-only hotplug briefly shows the row again
and the tool repairs it. The history only helps when nothing was applied
in between.

## Constraints

- DisplayPort MST daisy-chaining is a hard requirement (confirmed by the
  user on 2026-09-09). The machine has six physical DisplayPort outputs
  on the two discrete GPUs, and cabling every monitor directly would give
  stable connector names, but that option is off the table. Connector
  names must therefore be treated as unstable, and nothing may key on
  them.
- Applies are temporary. A persistent apply through D-Bus starts mutter's
  20 second "Keep changes" countdown and reverts the configuration if
  nobody confirms it, so an unattended daemon cannot use it. The price of
  temporary applies is a visible pass through the linear row after every
  hardware re-read, including resume from suspend, before the layout is
  repaired.
- Logical layout mode is in effect on this machine (`layout-mode` is
  `logical` and `supports-changing-layout-mode` is true). The tool reads
  the layout mode, sizes cells accordingly, and echoes it on apply only
  when mutter reports that it may be set; on a stock install without that
  capability the property is omitted and mutter's default applies.

## Goals

- Hold a chosen multi-monitor layout across monitor power cycles and
  hotplugs, without user action, for whatever subset of the pinned
  monitors is present.
- Identify monitors by EDID identity (vendor, product, serial), never by
  connector name.
- Leave deliberate changes alone: a rearrangement made in GNOME Settings
  or with `gdctl` is not reverted until the next hardware change, and its
  "Keep changes" confirmation keeps working. Running `save --force` after
  the change makes it the pinned layout.
- Stop `monitors.xml` from accumulating one stale configuration per
  connector rename.

## Non-goals

- Replacing GNOME Settings as the way to design a layout. The user
  arranges monitors in Settings; this tool captures and holds that
  arrangement.
- Multi-monitor logical monitors (mirroring), monitors leased to other
  compositors, color modes, backlight, or any X11 session. `save` refuses
  a mirrored layout; a mirror set up after saving is un-mirrored by the
  next repair.
- The GDM greeter, which runs its own shell with its own `monitors.xml`
  and stays a linear row.
- Editing `monitors.xml`.

## Behavior

### Layout file

`$XDG_CONFIG_HOME/gnome-monitor-pin/layout.json` (default
`~/.config/gnome-monitor-pin/layout.json`), overridable with `--layout`
or `GNOME_MONITOR_PIN_LAYOUT`. JSON, written by `save`, hand-editable.
One entry per monitor:

```json
{
  "monitors": [
    {
      "vendor": "DEL",
      "product": "DELL U2413",
      "serial": "FJMKT2CFA4GL",
      "x": 0,
      "y": 0,
      "scale": 1,
      "transform": "normal",
      "primary": true,
      "mode": "1920x1200@59.950"
    },
    {
      "vendor": "DEL",
      "product": "DELL U2413",
      "serial": "XTXXK4B4A1WL",
      "disabled": true
    }
  ]
}
```

Positions are logical pixels exactly as mutter reports them, so `save` is
lossless and the file mirrors what Settings shows. `mode` is optional. A
hand-written `scale` is snapped to the nearest scale the mode supports
(mutter reports scales as single-precision floats). A monitor that is
connected but switched off in Settings is recorded with `"disabled":
true` and no position, so the pin keeps it off. The file is written
atomically: a temporary file in the same directory, then a rename; a
symlink is followed so the real file is replaced.

### Commands

- `show` prints every connected monitor with its connector, identity,
  current position, mode, and primary flag, to stdout; a monitor that is
  connected but not shown is listed with the position `off`.
- `save` writes the current layout to the layout file, then prints the
  table it captured and a reminder that the arrangement should be made in
  Settings first. It refuses to overwrite an existing file unless `--force`
  is given, and warns when two captured monitors share an identity.
- `fix` applies the pinned layout once. `--dry-run` asks mutter to verify
  the derived configuration without applying it and prints, on stdout,
  each monitor's current and target position and every note the
  derivation made.
- `watch` runs until interrupted: it applies the layout at startup and
  again after every hardware change mutter reports, debounced so a burst
  of signals during MST re-enumeration produces one apply. It reloads the
  layout file for every apply, so `save --force` takes effect without a
  restart.

### Deriving the target from the pinned layout

Given the pinned layout and mutter's current state:

1. Match each connected monitor to a pinned entry by (vendor, product,
   serial). The connector name is taken from the current state and never
   from the file. When several connected monitors share one identity
   (mutter substitutes `unknown` for missing EDID fields), the k-th pinned
   entry with that identity maps to the k-th such monitor, monitors mutter
   is showing before ones it is not; the tool warns naming both
   connectors.
2. Decide which monitors take part. A pinned entry whose monitor is not
   connected is dropped and reported. A pinned entry marked disabled
   contributes nothing and keeps its monitor off, even after mutter's own
   fallback has switched it on. A monitor mutter lists but is not showing
   stays off unless the file pins it; a built-in panel mutter is not
   showing is treated as absent even when pinned, because a closed lid
   cannot be activated. A monitor leased to another compositor is ignored
   entirely.
3. Resolve each monitor's mode: the pinned mode by ID; else a mode with
   the same width and height and the closest refresh rate, which survives
   mutter renaming mode IDs, with a warning when the refresh differs by
   more than one hertz; else the monitor's preferred mode at the pinned
   position, with a warning. Then snap the pinned scale to the nearest
   scale the mode supports; if none is within tolerance, use the mode's
   preferred scale and warn.
4. Close empty bands. Project the remaining rectangles onto the x axis; any
   gap in that projection is a vertical band no present monitor occupies,
   and everything to its right shifts left by the gap width. Repeat on the
   y axis. Monitors otherwise stay where the file puts them. This is the
   approved "keep the others where they are, only close a fully empty row
   or column" policy.
5. Ensure the result is connected under mutter's rule: two logical
   monitors are adjacent only when they share an edge of positive length
   (a corner does not count), and every monitor must reach every other
   through adjacent ones. Band closing leaves some subsets disconnected,
   for example the top-left and bottom-right of the grid, or two-off
   combinations such as top-middle plus bottom-left. When that happens,
   compact rows then columns: monitors whose y ranges overlap form a row
   and close up leftward; monitors whose x ranges overlap form a column
   and close up upward; repeat until the layout is connected or a pass
   moves nothing. On the 2x3 grid one pass turns any disconnected survivor
   set into a compact block, and the lone cell of an L-shape lands at
   x = 0 under or over the leftmost survivor. Mixed sizes can need a
   second pass. If the layout is still disconnected, or two cells overlap
   because a fallback mode is larger than the pinned one, the derivation
   fails with a named error and nothing is applied.
6. Normalize so the bounding box starts at (0, 0); mutter rejects an
   offset region. Ensure exactly one primary: the first pinned primary
   that is present, else the present monitor with the smallest y, then
   the smallest x.
7. Append monitors mutter is showing that the file does not know about to
   the right of the pinned region, at the y of the topmost monitor on that
   right edge so they share an edge with it, in mutter's order, at their
   preferred mode and scale, keeping their current rotation, and log a
   warning for each. A monitor mutter is showing is never silently
   disabled; one the file marks disabled is switched off deliberately.

The result carries the state's serial, layout mode, and whether the
layout mode may be set, and a list of single-monitor logical monitors in
the shape `ApplyMonitorsConfig` takes. A monitor absent from that list is
disabled by mutter, which is how disabled entries take effect.

### Applying

- The derived target is compared with the current logical monitors on
  (connector, x, y, scale, transform, primary, mode), with scales compared
  to a small tolerance. When they are equal, nothing is applied. This
  makes `fix` idempotent and stops `watch` from reacting to the
  `MonitorsChanged` signal its own apply raises.
- Before applying, the target is submitted with the verify method. Mutter
  checks the serial first, for every method: a verify that fails because
  the state changed underneath it is retried from a fresh state, as is a
  call that timed out or a re-read that failed. Any other verify failure
  (for example "Logical monitors not adjacent") is a rejection: the tool
  reports mutter's reason and leaves mutter's layout alone rather than
  guessing. A rejection is never retried.
- The apply uses the temporary method, so mutter does not write
  `monitors.xml`. The pinned layout is the tool's file, not mutter's.
- `ApplyMonitorsConfig` carries the serial from the `GetCurrentState`
  call the target was derived from, and the layout mode when mutter
  allows setting it. A stale serial or any other transport failure is
  retried from a fresh state up to three times with a short delay.
- Every D-Bus call, the match subscriptions included, has a 30 second
  timeout, so a compositor that stops answering cannot stall the watch
  loop silently.

### Watch loop

- Subscribe on the session bus to `MonitorsChanged` from
  `org.gnome.Mutter.DisplayConfig`, and to `NameOwnerChanged` for that
  bus name, so a shell that appears after the daemon started triggers a
  fix.
- Fix once at startup and remember the configuration serial mutter
  reports.
- Each signal resets a debounce timer (default one second, flag
  `--debounce`). When it fires, read the state; if the serial is unchanged,
  do nothing. Mutter increments the serial only when it re-reads the
  hardware (hotplug, MST re-enumeration, resume from suspend, a virtual
  monitor created or destroyed for screen sharing), never when a
  configuration is applied by Settings, `gdctl`, a shell extension, or
  this tool, and not for gamma or privacy-screen changes. So a changed
  serial means hardware changed and the pinned layout is re-applied; an
  unchanged serial means someone chose the current layout and it is left
  alone. Known limits of that rule, none reachable on this desktop: a lid
  open or close and a change of virtual-monitor mode reconfigure without
  a re-read, so they are left alone too. A read that fails is treated as
  a change, so the fix's own retries take over.
- The rule rests on mutter's implementation, not on its documented
  interface. If a future mutter bumps the serial on every apply, the
  symptom is a Settings change snapping back within the debounce; every
  repair log line names the connectors that appeared or disappeared, so a
  repair with none of either is the tell, and the README says so.
- Each repair logs one line at info with the old and new serial and the
  connectors that appeared or disappeared since the last state the daemon
  saw; the serial comparison itself is logged at debug. Missing pinned
  monitors, appended unknown ones, and derivation warnings are logged only
  when a layout is applied, so a quiet desktop produces no log noise.
- A layout-file error (missing, unparseable, empty) is logged, naming the
  path, and the loop continues; the file is reloaded at the next fix. The
  state a failed fix saw is still remembered when it carries a serial, so
  a deliberate change after a failed derivation is not mistaken for
  hardware.
- If the bus connection closes, `watch` returns an error and exits
  non-zero; the systemd unit restarts it. On Wayland a shell exit ends the
  session, and the unit is bound to the session target, so this is the
  crash case only. Cancellation through the context ends the loop cleanly
  with exit status zero and without an error in the log.

### Systemd

`contrib/gnome-monitor-pin.service` is a user unit, `Type=exec`, bound to
`graphical-session.target` (which on this machine is ordered after
gnome-shell reports ready), conditioned on `WAYLAND_DISPLAY` being set in
the user manager's environment, running `gnome-monitor-pin watch` with
text logs and `Restart=on-failure`. The README explains installing it with
`systemctl --user enable --now`, that a failed condition leaves the unit
inactive rather than failed, changing the layout later (arrange in
Settings, answer the Keep dialog, then `save --force`), stopping it,
reading its journal, recovering a unit that hit the start limit, running
a second copy in a terminal, and that a hotplug, a resume, and a login
show the linear row for about a second before the grid returns.

### Packaging

Every release publishes RPMs for Fedora 43 and Fedora 44, x86_64, as
assets of the GitHub release (GitHub Packages has no RPM repository
type, so the release is where `dnf install ./file.rpm` gets them). The
release workflow's goreleaser job builds and stamps the binary; an `rpm`
job then runs once per Fedora version inside that Fedora's container
image, packages the binary with a spec file kept in `packaging/`, and
uploads `gnome-monitor-pin-<version>-1.fc<NN>.x86_64.rpm`. The RPM
installs the binary in `/usr/bin` and the user unit in
`/usr/lib/systemd/user`, so `systemctl --user enable --now
gnome-monitor-pin` works with no copying. The unit therefore names
`/usr/bin/gnome-monitor-pin`; a `go install` user overrides `ExecStart`
with `systemctl --user edit`. Each RPM is installed in a clean container
of its Fedora version and `gnome-monitor-pin --version` must report the
tag before the asset is uploaded.

## Architecture

Go 1.27, per the go-conventions plugin: cobra and viper for the CLI,
`log/slog` JSON to stderr, Ginkgo and Gomega for tests, counterfeiter for
fakes.

Packages, one per concern, all under `internal/`:

- `layout`: the pure model and rules. Types for monitor identity, pinned
  placements (including disabled ones), mutter's current state (each
  mode's supported scales, whether a monitor is built in or leased, the
  layout mode and whether it may be set), and the derived target.
  `Derive` implements the steps above, including mutter's adjacency and
  connectivity rules; `Equal` implements the idempotency comparison;
  `Snapshot` and `Capture` build placements from a current state for
  `show` and `save`. No I/O, no D-Bus. Fully unit-tested with tables,
  including a property check that every subset of the grid, and of a
  mixed-scale variant of it, derives a layout mutter's rules accept.
- `displayconfig`: the D-Bus client. Decodes `GetCurrentState` into
  `layout.State`, encodes a target into the `ApplyMonitorsConfig`
  arguments (echoing the layout mode when allowed), and exposes a
  subscription that coalesces `MonitorsChanged` and `NameOwnerChanged`
  into one event channel. Uses `github.com/godbus/dbus/v5` on the session
  bus with a per-call timeout, sender-restricted match rules, and no
  auto-start. The codec is tested without a bus by storing hand-built
  D-Bus values in the shape godbus's decoder produces and by checking the
  encoded signature.
- `pin`: orchestration. `Fix` runs load, state, derive, compare, verify,
  apply with retries; `Watch` runs the serial-gated debounced loop. It
  declares the narrow interface it needs (`CurrentState`, `Verify`,
  `Apply`, `Subscribe`) and takes the layout through a loader function so
  the file is re-read per fix; the counterfeiter fake for that interface
  lives beside it. Tested against the fake, including the no-op path, the
  stale-serial retry, the rejection path, the serial gate, and the
  debounce reset.
- `cli`: the cobra tree and the layout-file store (read, atomic write with
  refuse-to-overwrite).
- `cmd/gnome-monitor-pin`: the thin main from the template.

The `--layout` path, `--debounce`, `--dry-run`, and `--force` are viper
keys under the `GNOME_MONITOR_PIN_` prefix like every other flag.

## Errors

- No layout file: `fix` fails with a message that names the path and says
  to run `save`; `watch` logs the same and keeps running.
- A layout file naming no monitor mutter shows: error, nothing applied.
- A derived layout that cannot be made connected, or whose fallback mode
  overlaps a neighbor: error naming the monitors, nothing applied.
- Mutter unreachable on the session bus: error naming the bus name.
- Verify rejected: `fix` exits non-zero with mutter's reason; `watch` logs
  it at warn and keeps watching.

## Testing

- `layout`: tables covering the full grid, each single monitor missing,
  an entire column missing, an entire row missing, the four two-off
  combinations band closing leaves disconnected, the diagonal pair, every
  subset of the grid and of a mixed-scale grid checked against a mirror
  of mutter's rules, a layout offset into negative coordinates, the
  pinned primary missing and a later pinned primary present, an active
  unknown monitor present, an inactive monitor present, a pinned disabled
  monitor that mutter's fallback switched on, a built-in panel mutter is
  not showing, a leased monitor, duplicate identities with the shown one
  first, a pinned mode the monitor no longer offers by ID but does by
  resolution, the closest refresh among several same-resolution modes, a
  refresh far from the pinned one, a pinned mode with no matching
  resolution kept in place, a fallback mode that overlaps a neighbor, a
  hand-written scale that snaps, a scale the mode does not support, a
  rotated monitor, physical layout mode, and the idempotency comparison
  on position, mode, primary, transform, scale, and mirroring.
- `displayconfig`: decode from the nested-slice shape godbus produces,
  including the built-in, lease, and layout-mode properties, and the
  encoded signature and properties of an apply with and without the
  layout-mode capability.
- `pin`: fake-backed specs for the apply, no-op, dry-run, rejection,
  stale-serial verify, failed re-read, timeout, retry, and give-up paths;
  the serial gate (a signal with an unchanged serial applies nothing, a
  changed serial applies, a rejected state is remembered, a failed gate
  read falls through to the fix), the debounce reset under a spaced
  burst, the layout reload, layout errors that keep the loop alive,
  disconnect, and cancellation.
- `cli`: each command against a fake display, including that `watch`
  subscribes and attempts a fix without a layout file, that `save` leaves
  no temporary file behind, and that `show` lists a switched-off monitor.
- An integration spec behind the `integration` build tag connects to the
  real session bus and runs a dry-run fix, so the real codec is exercised
  without changing the desktop.
- Manual acceptance: `save` on the correct grid, power one monitor off and
  on, observe `watch` restore the grid; suspend and resume; rearrange in
  Settings and confirm the daemon leaves it alone.
