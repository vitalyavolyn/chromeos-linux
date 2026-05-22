{ pkgs }:
pkgs.buildGoModule {
  pname = "chromeos-linux-bridges";
  version = "0.1.1";
  src = ../dbus-bridges;
  vendorHash = "sha256-Ac63bZlBvCrhS7b8mk7aJdApI8UGtJxnZG35L37roGY=";
  subPackages = [
    "cmd/session-bridge"
    "cmd/shill-bridge"
    "cmd/power-bridge"
    "cmd/cras-bridge"
    "cmd/crostini-bridge"
    "cmd/register-apps"
    "cmd/rmad-bridge"
    "cmd/mojo-stub"
  ];
}
