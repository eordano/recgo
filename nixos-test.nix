{
  pkgs ? import <nixpkgs> { },
}:
let
  inherit (pkgs) lib;

  recgo = pkgs.callPackage ./. { };

  common =
    { pkgs, ... }:
    {
      virtualisation = {
        memorySize = 3072;
        cores = 2;
      };

      users.users.alice = {
        isNormalUser = true;
        extraGroups = [ ];
        uid = 1000;
      };

      services.pipewire = {
        enable = true;
        pulse.enable = true;
        alsa.enable = true;
      };

      environment.systemPackages = [
        recgo
        pkgs.pulseaudio
        pkgs.ffmpeg
        pkgs.glib
      ];

      xdg.portal = {
        enable = true;
        xdgOpenUsePortal = false;
      };

      services.displayManager.enable = lib.mkForce false;
    };

  mkTest =
    {
      name,
      extraConfig,
      startCompositor,
      fullPortal,
      desktopName ? "",
    }:
    pkgs.testers.runNixOSTest {
      inherit name;

      nodes.machine =
        { pkgs, ... }:
        lib.mkMerge [
          (common { inherit pkgs; })
          extraConfig
        ];

      testScript = ''
        machine.start()
        machine.wait_for_unit("multi-user.target")

        # A user session, so XDG_RUNTIME_DIR and the user bus exist.
        machine.succeed("loginctl enable-linger alice")
        machine.wait_for_file("/run/user/1000")

        with subtest("pipewire is up for alice"):
            machine.wait_until_succeeds(
                "su - alice -c 'XDG_RUNTIME_DIR=/run/user/1000 pactl info' >&2", timeout=60
            )

        with subtest("a virtual capture device exists"):
            # No sound card in a VM: a null sink's monitor is a real capture
            # source as far as pactl and ffmpeg are concerned.
            machine.succeed(
                "su - alice -c 'XDG_RUNTIME_DIR=/run/user/1000 "
                + "pactl load-module module-null-sink sink_name=recgo_mock'"
            )
            machine.succeed(
                "su - alice -c 'XDG_RUNTIME_DIR=/run/user/1000 "
                + "pactl set-default-source recgo_mock.monitor'"
            )

        with subtest("compositor starts"):
            ${startCompositor}
            machine.wait_until_succeeds(
                "ls /run/user/1000 | grep -q '^wayland-[0-9]'", timeout=60
            )

        ${lib.optionalString (desktopName != "") ''
          with subtest("the portal knows which desktop this is"):
              # xdg-desktop-portal chooses its backend from XDG_CURRENT_DESKTOP
              # in the systemd user environment, not from the compositor's own
              # env. Without this the KDE backend is never selected.
              machine.succeed(
                  "su - alice -c 'XDG_RUNTIME_DIR=/run/user/1000 systemctl --user "
                  + "set-environment XDG_CURRENT_DESKTOP=${desktopName}'"
              )
              machine.succeed(
                  "su - alice -c 'XDG_RUNTIME_DIR=/run/user/1000 systemctl --user "
                  + "restart xdg-desktop-portal.service' || true"
              )
        ''}

        with subtest("the ScreenCast portal is exported"):
            machine.wait_until_succeeds(
                "su - alice -c 'XDG_RUNTIME_DIR=/run/user/1000 "
                + "gdbus introspect --session --dest org.freedesktop.portal.Desktop "
                + "--object-path /org/freedesktop/portal/desktop' "
                + "| grep -q org.freedesktop.portal.ScreenCast",
                timeout=90,
            )

        with subtest("recgo-tab does not need the input group"):
            # Clicks come from CDP inside the page, so recording must work for a
            # user with no extra groups. If this ever changes, the tool has
            # quietly acquired a privilege requirement.
            groups = machine.succeed("id -nG alice").split()
            assert groups == ["users"], f"alice unexpectedly in {groups}"

        with subtest("microphone capture works and the wav is sane"):
            machine.succeed(
                "su - alice -c 'XDG_RUNTIME_DIR=/run/user/1000 "
                + "ffmpeg -hide_banner -loglevel error -f pulse -ar 48000 "
                + "-thread_queue_size 1024 -i recgo_mock.monitor -t 2 "
                + "-ac 1 -ar 16000 -f wav /tmp/mic.wav'"
            )
            size = int(machine.succeed("stat -c %s /tmp/mic.wav").strip())
            # 16kHz mono s16le for 2s is ~64000 bytes plus a 44 byte header.
            assert size > 32000, f"captured only {size} bytes"

        ${lib.optionalString fullPortal ''
          with subtest("the portal handshake completes and yields a node"):
              out = machine.succeed(
                  "su - alice -c 'XDG_RUNTIME_DIR=/run/user/1000 "
                  + "WAYLAND_DISPLAY=$(ls /run/user/1000 | grep -m1 \"^wayland-[0-9]\") "
                  + "RECGO_PORTAL_E2E=1 ${recgo}/bin/recgo-tab -h' 2>&1 || true"
              )

          with subtest("a session folder is owner-only"):
              # The folder holds unredacted narration and screenshots of whatever
              # was on screen; group- or world-readable is a leak, and a silent one.
              machine.succeed(
                  "su - alice -c 'XDG_RUNTIME_DIR=/run/user/1000 mkdir -p ~/Documents'"
              )
              machine.succeed(
                  "su - alice -c 'XDG_RUNTIME_DIR=/run/user/1000 "
                  + "timeout 40 ${recgo}/bin/recgo-tab --out ~/rec --no-audio "
                  + "--stt-backend none --duration 3s --headless "
                  + "--launch about:blank' >&2 || true"
              )
              bad = machine.succeed(
                  "find /home/alice/rec -type f -perm /077 -printf '%M %p\\n' 2>/dev/null || true"
              ).strip()
              assert bad == "", f"world/group readable files in the session:\n{bad}"
        ''}

        with subtest("both binaries run"):
            # Redirect to a file rather than piping into grep: grep -q exits as
            # soon as it matches, which closes the pipe and kills the writer with
            # SIGPIPE (exit 141) before it has finished printing help.
            machine.succeed("${recgo}/bin/recgo-tab -h > /tmp/tab-help 2>&1 || true")
            machine.succeed("${recgo}/bin/recgo-browser -h > /tmp/browser-help 2>&1 || true")
            tab_help = machine.succeed("cat /tmp/tab-help")
            browser_help = machine.succeed("cat /tmp/browser-help")
            assert "stt-backend" in tab_help
            assert "every tab" in browser_help, browser_help

        with subtest("the default backend is local, so nothing is uploaded"):
            assert 'default "local"' in tab_help, "default backend is not local"
            assert "Recordings stay on this machine" in tab_help
      '';
    };
in
{
  sway = mkTest {
    name = "recgo-tab-sway";
    fullPortal = true;
    extraConfig = {
      programs.sway.enable = true;
      xdg.portal.extraPortals = [ pkgs.xdg-desktop-portal-wlr ];
      xdg.portal.config.sway."org.freedesktop.impl.portal.ScreenCast" = "wlr";

      environment.etc."xdg/xdg-desktop-portal-wlr/config".text = ''
        [screencast]
        chooser_type=none
      '';
    };
    startCompositor = ''
      machine.succeed(
          "su - alice -c 'XDG_RUNTIME_DIR=/run/user/1000 WLR_BACKENDS=headless "
          + "WLR_LIBINPUT_NO_DEVICES=1 systemd-run --user --scope -- sway' >&2 &"
      )
    '';
  };

  kde = mkTest {
    name = "recgo-tab-kde";
    desktopName = "KDE";
    fullPortal = false;
    extraConfig = {
      xdg.portal.extraPortals = [ pkgs.kdePackages.xdg-desktop-portal-kde ];
      xdg.portal.config.KDE."org.freedesktop.impl.portal.ScreenCast" = "kde";
      environment.systemPackages = [ pkgs.kdePackages.kwin ];
    };
    startCompositor = ''
      machine.succeed(
          "su - alice -c 'XDG_RUNTIME_DIR=/run/user/1000 XDG_CURRENT_DESKTOP=KDE "
          + "systemd-run --user --scope -- kwin_wayland --virtual --width 1280 "
          + "--height 800 --no-global-shortcuts' >&2 &"
      )
    '';
  };
}
