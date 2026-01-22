Name:           kayaknet
Version:        1.0.0
Release:        1%{?dist}
Summary:        Anonymous Encrypted Network

License:        MIT
URL:            https://kayaknet.io
Source0:        %{name}-%{version}.tar.gz

BuildArch:      noarch
BuildRequires:  python3-devel
Requires:       python3 >= 3.8
Requires:       python3-gobject
Requires:       gtk3

%description
KayakNet is a decentralized anonymous network for private communication
and commerce.

Features:
- End-to-end encrypted messaging
- Anonymous marketplace with crypto escrow
- .kyk domain registration
- Onion routing for traffic analysis resistance

%prep
%autosetup

%install
mkdir -p %{buildroot}%{_bindir}
mkdir -p %{buildroot}%{_datadir}/applications
mkdir -p %{buildroot}%{_datadir}/icons/hicolor/256x256/apps

install -m 755 kayaknet-gui.py %{buildroot}%{_bindir}/kayaknet-gui

cat > %{buildroot}%{_datadir}/applications/kayaknet.desktop << EOF
[Desktop Entry]
Name=KayakNet
Comment=Anonymous Encrypted Network
Exec=/usr/bin/kayaknet-gui
Icon=kayaknet
Terminal=false
Type=Application
Categories=Network;InstantMessaging;Security;
Keywords=anonymous;encrypted;chat;marketplace;privacy;
StartupNotify=true
EOF

%post
update-desktop-database %{_datadir}/applications &> /dev/null || :
gtk-update-icon-cache %{_datadir}/icons/hicolor &> /dev/null || :

%postun
update-desktop-database %{_datadir}/applications &> /dev/null || :
gtk-update-icon-cache %{_datadir}/icons/hicolor &> /dev/null || :

%files
%{_bindir}/kayaknet-gui
%{_datadir}/applications/kayaknet.desktop

%changelog
* Wed Jan 22 2026 KayakNet Team <team@kayaknet.io> - 1.0.0-1
- Initial release

