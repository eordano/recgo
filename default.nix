{
  lib,
  stdenv,
  buildGoModule,
  ffmpeg-full,
  chromium,
  whisper-cpp,
  gst_all_1,
  pipewire,
  pulseaudio,
  rsync,
  openssh,
  switchaudio-osx,
  makeWrapper,
}:

let
  ffmpeg = ffmpeg-full.override {
    withWhisper = false;
    withFrei0r = false;
  };
in
buildGoModule rec {
  pname = "recgo";
  version = "0.1.0";

  src = ./.;

  # Deps are vendored in-tree so the build needs no network.
  vendorHash = null;

  nativeBuildInputs = [ makeWrapper ];

  __darwinAllowLocalNetworking = true;

  gstPlugins = lib.optionalString stdenv.isLinux (
    lib.makeSearchPath "lib/gstreamer-1.0" [
      gst_all_1.gstreamer
      gst_all_1.gst-plugins-base
      gst_all_1.gst-plugins-good
      pipewire
    ]
  );

  postInstall = ''
        wrapProgram $out/bin/recgo \
          --prefix PATH : ${
            lib.makeBinPath (
              [
                ffmpeg
                rsync
                openssh
              ]
              ++ lib.optional stdenv.isLinux pulseaudio
              ++ lib.optional stdenv.isDarwin switchaudio-osx
            )
          }

        # recgo-tab additionally drives a browser over CDP and transcribes locally.
        # Chromium and whisper-cpp are runtime deps rather than build ones, and
        # deliberately only PATH fallbacks: --chromium and --whisper-bin let you
        # point at your own. whisper-cpp is what makes the default backend local --
        # recgo-tab does not upstream recordings, so transcription has to work
        # without a network.
        # KWin refuses org.kde.KWin.ScreenShot2 unless the caller resolves to a
        # desktop file declaring X-KDE-DBUS-Restricted-Interfaces -- it maps
        # /proc/PID/exe back to an Exec= line, so the path must be absolute.
        # Without it the KDE screenshot path fails with "The process is not
        # authorized to take a screenshot".
        mkdir -p $out/share/applications
        for tabBin in recgo-tab recgo-browser recgo-desktop; do
          cat > $out/share/applications/$tabBin.desktop <<EOF
    [Desktop Entry]
    Type=Application
    Name=$tabBin
    NoDisplay=true
    Exec=$out/bin/$tabBin %f
    X-KDE-DBUS-Restricted-Interfaces=org.kde.KWin.ScreenShot2
    EOF
        done

        for tabBin in recgo-tab recgo-browser recgo-desktop; do
          wrapProgram $out/bin/$tabBin \
            --prefix PATH : ${
              lib.makeBinPath (
                [ ffmpeg ]
                ++ lib.optional stdenv.isLinux chromium
                ++ [ whisper-cpp ]
                ++ lib.optionals stdenv.isLinux [
                  gst_all_1.gstreamer
                  pulseaudio
                ]
                ++ lib.optional stdenv.isDarwin switchaudio-osx
              )
            } ${lib.optionalString stdenv.isLinux ''
              \
                --prefix GST_PLUGIN_SYSTEM_PATH_1_0 : ${
                  lib.makeSearchPath "lib/gstreamer-1.0" [
                    gst_all_1.gstreamer
                    gst_all_1.gst-plugins-base
                    gst_all_1.gst-plugins-good
                    pipewire
                  ]
                }''}
        done
  '';

  meta = with lib; {
    description = "A btop-inspired terminal audio recorder (plus recgo-tab, a browser-tab recorder)";
    homepage = "https://github.com/eordano/recgo";
    license = licenses.mit;
    maintainers = [ ];
    platforms = platforms.unix;
    mainProgram = "recgo";
  };
}
