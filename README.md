# gnome-monitor-pin

Pin a GNOME multi-monitor layout across monitor hotplugs.

## Why

GNOME remembers a monitor arrangement for the exact set of monitors it was
made for, and identifies each monitor by its connector name as well as its
EDID. Two things go wrong on a desk with DisplayPort MST daisy chains:

- When a monitor is powered off, no arrangement was ever saved for the
  monitors that remain, and mutter has no rule for keeping the survivors
  where they are, so it lays them out in one long row.
- When it is powered on again, its MST hub re-enumerates and the kernel
  gives it a new connector name, so the saved arrangement for the full set
  no longer matches and mutter lays everything out in a row again.

This tool records the arrangement keyed on vendor, product, and serial, and
puts it back for whatever subset is present whenever mutter reports a
hardware change. Upstream tracks the connector-name half as mutter issue
#4036 and merge request !984; nothing upstream addresses the survivor half.

## Install

From a release: download the RPM for your Fedora version from the release
page and install it.

```sh
sudo dnf install ./gnome-monitor-pin-*.fc43.x86_64.rpm
```

With Go: `go install github.com/jhoblitt/gnome-monitor-pin/cmd/gnome-monitor-pin@latest`,
then copy `contrib/gnome-monitor-pin.service` to `~/.config/systemd/user/`
and point it at the binary:

```sh
systemctl --user edit gnome-monitor-pin
# add:
# [Service]
# ExecStart=
# ExecStart=%h/go/bin/gnome-monitor-pin watch
```

## Usage

1. Arrange the monitors in GNOME Settings, apply, and click Keep Changes.
2. Check what mutter shows: `gnome-monitor-pin show`. A monitor you
   switched off in Settings is listed as `off`.
3. Record it: `gnome-monitor-pin save`. It lands in
   `~/.config/gnome-monitor-pin/layout.json` and prints what it captured.
4. Check the derived configuration against the live session without
   changing it: `gnome-monitor-pin fix --dry-run`. It prints each
   monitor's current and target position and every note the derivation
   made.
5. Hold it: `systemctl --user enable --now gnome-monitor-pin`.

`gnome-monitor-pin fix` applies the pinned layout once. Run commands in a
terminal with `--log-format text` for readable logs.

After a hotplug, a resume from suspend, or a login, the desktop shows
mutter's row for about a second, then the pinned layout returns; windows
may move twice. `--debounce` on `watch` trades that second against extra
applies during a slow MST re-enumeration.

When a pinned monitor is off, the others stay where they are; a column or
row left completely empty closes up, and survivors that would only touch
at corners (which mutter refuses) close up into a block. A few survivor
sets of a mixed-size layout cannot be compacted into a connected block;
the tool logs that and leaves mutter's layout alone. A monitor mutter
shows that the layout does not know is placed to the right of the pinned
ones, never disabled; a monitor recorded as disabled stays off even after
mutter's own fallback switches it on. Applies are temporary, so mutter's
own `monitors.xml` is left alone.

## Changing the layout

`watch` only reacts to hardware changes, so rearrange in Settings as usual,
answer the Keep Changes dialog, then run `gnome-monitor-pin save --force`.
The running service reads the file again at its next apply; no restart is
needed. Until you save, the next hardware change puts the pinned layout
back.

## Stopping

```sh
systemctl --user disable --now gnome-monitor-pin
```

## Troubleshooting

- Logs: `journalctl --user -u gnome-monitor-pin -f`. Add
  `--log-level debug` (or `GNOME_MONITOR_PIN_LOG_LEVEL=debug` in the unit)
  to see every signal and the serial comparison. Every repair logs which
  connectors appeared or disappeared; at startup that list is empty.
- `systemctl --user status` says `inactive` with a condition failed: the
  unit only runs inside a Wayland session, so enabling it from SSH does
  nothing until the next login.
- The unit is `failed` with `start-limit-hit`: the binary was missing when
  it started five times in ten seconds. Fix the cause, then
  `systemctl --user reset-failed gnome-monitor-pin` and
  `systemctl --user start gnome-monitor-pin`. A missing layout file is
  not fatal: the service logs it and waits.
- A change made in Settings snaps back within a second: that would mean
  this mutter bumps its configuration serial on applies, which this tool
  relies on it not doing. The repair line in the journal will name no
  added or removed connectors. Stop the service and report the mutter
  version.
- `fix` says mutter rejected the configuration: the message carries
  mutter's reason (for example a scale a mode does not support). `show`
  prints what mutter shows now, and `fix --dry-run` prints what was
  derived from the layout file.
- Two monitors report the same vendor, product, and serial: `save` warns,
  and `fix` tells them apart by the order mutter lists them, which can
  swap after a re-enumeration.
- Running `watch` in a terminal while the service runs is harmless: both
  see the same serial and the second one finds nothing to do.
- The GDM login screen has its own GNOME session and its own saved
  arrangement; this tool cannot reach it.

Every flag has an environment form under `GNOME_MONITOR_PIN_`, for
example `GNOME_MONITOR_PIN_LAYOUT` and `GNOME_MONITOR_PIN_LOG_LEVEL`.

The integration spec talks to the real session bus with a verify-only
apply: `go test -tags integration ./internal/displayconfig/...` from a
shell inside a GNOME session.
