# Built by packaging/build-rpm.sh from a release binary; there is no
# %prep or %build because the binary is goreleaser's, already stamped
# with the tag.
#
# The binary is prebuilt and stripped, so there is nothing to extract.
%global debug_package %{nil}

Name:           gnome-monitor-pin
Version:        %{version}
Release:        1%{?dist}
Summary:        Pin a GNOME multi-monitor layout across monitor hotplugs
License:        Apache-2.0
URL:            https://github.com/jhoblitt/gnome-monitor-pin
Source0:        gnome-monitor-pin
Source1:        gnome-monitor-pin.service
Source2:        README.md
Source3:        LICENSE
BuildArch:      x86_64
BuildRequires:  systemd-rpm-macros
%{?systemd_requires}

%description
gnome-monitor-pin records a GNOME multi-monitor arrangement keyed on each
monitor's EDID identity and re-applies it whenever mutter re-reads the
hardware, so a DisplayPort MST hotplug no longer collapses the desktop
into a single row. It ships a systemd user unit; enable it with
systemctl --user enable --now gnome-monitor-pin.

%install
install -D -m 0755 %{SOURCE0} %{buildroot}%{_bindir}/gnome-monitor-pin
install -D -m 0644 %{SOURCE1} %{buildroot}%{_userunitdir}/gnome-monitor-pin.service
install -D -m 0644 %{SOURCE2} %{buildroot}%{_docdir}/%{name}/README.md
install -D -m 0644 %{SOURCE3} %{buildroot}%{_licensedir}/%{name}/LICENSE

%post
%systemd_user_post gnome-monitor-pin.service

%preun
%systemd_user_preun gnome-monitor-pin.service

%postun
%systemd_user_postun_with_restart gnome-monitor-pin.service

%files
%{_bindir}/gnome-monitor-pin
%{_userunitdir}/gnome-monitor-pin.service
%doc %{_docdir}/%{name}/
%license %{_licensedir}/%{name}/

%changelog
* Wed Sep 09 2026 Joshua Hoblitt <josh@hoblitt.com> - %{version}-1
- Built from the release archive.
