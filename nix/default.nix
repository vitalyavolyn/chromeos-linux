{ config, lib, pkgs, ... }:

let
  cfg = config.services.chromeos-linux;

  mkDbusPolicy = user: pkgs.runCommand "chromeos-dbus-policy" {} ''
    mkdir -p $out/share/dbus-1/system.d
    sed 's|<policy user="ash">|<policy user="${user}">|' \
      ${../session/dbus-policies/chromeos-bridges.conf} \
      > $out/share/dbus-1/system.d/chromeos-bridges.conf
  '';

  bridgesPkg = import ./bridges.nix { inherit pkgs; };

  vshStub = pkgs.writeShellScript "vsh" ''
    exec ${pkgs.bashInteractive}/bin/bash -i
  '';

  mkBridgeService = name:
  let
    startScript = pkgs.writeShellScript "${name}-bridge-start" (
      lib.optionalString (name != "power") ''
        _uid=$(id -u)
        export PULSE_SERVER="unix:/run/user/$_uid/pulse/native"
        export XDG_RUNTIME_DIR="/run/user/$_uid"
      '' + ''
        exec ${bridgesPkg}/bin/${name}-bridge
      ''
    );
  in {
    description = "ChromeOS ${name} D-Bus bridge";
    wantedBy = [ "multi-user.target" ];
    before = [ "display-manager.service" ];
    after = [ "dbus.service" ];
    requires = [ "dbus.service" ];
    serviceConfig = {
      ExecStart = "${startScript}";
      Restart = "on-failure";
      RestartSec = "2s";
    } // lib.optionalAttrs (name != "power") {
      User = cfg.user;
      Group = "users";
    };
  };
in {
  options.services.chromeos-linux = {
    enable = lib.mkEnableOption "ChromeOS Ash shell on Linux";

    user = lib.mkOption {
      type = lib.types.str;
      default = "ash";
      description = "User to run bridge services as. Should be the user running the Ash session.";
    };

    bridges = lib.mkOption {
      type = lib.types.listOf (lib.types.enum [
        "shill" "cras" "power" "session" "crostini" "rmad"
      ]);
      default = [ "shill" "cras" "power" "session" "crostini" "rmad" ];
      description = "Which D-Bus bridges to enable.";
    };

    sessionPackage = lib.mkOption {
      type = lib.types.nullOr lib.types.package;
      default = null;
      description = "The chromeos-linux-session package (from flake outputs.packages.\${system}.chromeosLinuxSession).";
    };
  };

  config = lib.mkIf cfg.enable {
    services.dbus.packages = [ (mkDbusPolicy cfg.user) ];
    services.upower.enable = true;
    security.polkit.enable = true;

    environment.etc."lsb-release".text = lib.mkForce ''
      DISTRIB_ID=nixos
      DEVICETYPE=CHROMEBOOK
      CHROMEOS_RELEASE_NAME=Chrome OS
    '';

    environment.pathsToLink = [ "/share/applications" "/share/pixmaps" "/share/icons" ];

    systemd.tmpfiles.rules = [
      "L+ /usr/bin/vsh   - - - - ${vshStub}"
      "L+ /usr/bin/crosh - - - - ${vshStub}"
      "d /var/lib/ash-profile                                      0700 ${cfg.user} users -"
      "d /var/lib/ash-profile/test-user                            0700 ${cfg.user} users -"
      "d /run/mojo                                                  0755 ${cfg.user} users -"
      "d /home/chronos                                              0755 root        root  -"
      "d /home/chronos/user                                         0755 ${cfg.user} users -"
      "d /var/lib/metrics/structured/chromium/storage/flushed       0755 ${cfg.user} users -"
      "d /var/log/chrome                                            0700 ${cfg.user} users -"
      "d /var/lib/dlc/termina-dlc/package/root                     0755 root root -"
      "d /run/imageloader/cros-termina/99999.0.0                   0755 root root -"
      "f /run/imageloader/cros-termina/99999.0.0/vm_kernel         0644 root root -"
      "f /run/imageloader/cros-termina/99999.0.0/vm_rootfs.img     0644 root root -"
      "d /run/daemon-store/crosvm/test-user                        0755 ${cfg.user} users -"
      "f /run/daemon-store/crosvm/test-user/termina.img            0644 ${cfg.user} users -"
    ];

    xdg.portal = {
      enable = true;
      extraPortals = [ pkgs.xdg-desktop-portal-gtk ];
      config.common.default = "gtk";
    };
    services.gnome.gnome-keyring.enable = true;
    environment.systemPackages = with pkgs; [
      gnome-keyring
      polkit_gnome
      xdg-desktop-portal
      xdg-desktop-portal-gtk
      librsvg
    ];

    systemd.services = {
      mojo-stub = {
        description = "ChromeOS Mojo service manager socket stub";
        wantedBy = [ "multi-user.target" ];
        before = [ "display-manager.service" ];
        after = [ "dbus.service" ];
        serviceConfig = {
          ExecStart = "${bridgesPkg}/bin/mojo-stub";
          Restart = "on-failure";
          RestartSec = "2s";
          User = cfg.user;
          Group = "users";
        };
      };
    } // lib.listToAttrs (map (bridge: {
      name = "${bridge}-bridge";
      value = mkBridgeService bridge // lib.optionalAttrs (bridge == "shill") {
        after = [ "dbus.service" "NetworkManager.service" ];
        requires = [ "dbus.service" "NetworkManager.service" ];
      } // lib.optionalAttrs (bridge == "power") {
        after = [ "dbus.service" "upower.service" ];
        requires = [ "dbus.service" "upower.service" ];
      };
    }) cfg.bridges) // lib.optionalAttrs (builtins.elem "cras" cfg.bridges) {
      cras-bridge = (mkBridgeService "cras") // {
        path = [ pkgs.pulseaudio ];
      };
    };

    services.xserver.windowManager.session = lib.mkIf (cfg.sessionPackage != null) [{
      name = "chromeos-ash";
      start = ''
        cp "$XAUTHORITY" /tmp/ash-xauth 2>/dev/null || true
        chmod 644 /tmp/ash-xauth 2>/dev/null || true
        exec ${cfg.sessionPackage}/bin/chromeos-linux-session
      '';
    }];
  };
}
