{
  lib,
  stdenv,
  stdenvNoCC,
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
  binary = buildGoModule {
    pname = "recgo";
    version = "0.1.0";
    src = lib.fileset.toSource {
      root = ./.;
      fileset = lib.fileset.unions [
        ./go.mod
        ./go.sum
        ./cmd
        ./internal
      ];
    };

    vendorHash = "sha256-CSO0qBt/87wAygdcOOs5FuL5tlrjE9yUMs1E5Z+3TV4=";

    __darwinAllowLocalNetworking = true;
  };
in
stdenvNoCC.mkDerivation rec {
  pname = "recgo";
  inherit (binary) version;
  dontUnpack = true;
  dontConfigure = true;
  dontBuild = true;
  passthru = { inherit binary; };
  installPhase = ''
    runHook preInstall
    mkdir -p $out
    cp -r ${binary}/bin $out/bin
    chmod -R u+w $out/bin
    runHook postInstall
  '';

  nativeBuildInputs = [ makeWrapper ];

  __darwinAllowLocalNetworking = true;

  gstPlugins = lib.optionalString stdenv.hostPlatform.isLinux (
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
              ++ lib.optional stdenv.hostPlatform.isLinux pulseaudio
              ++ lib.optional stdenv.hostPlatform.isDarwin switchaudio-osx
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
        for tabBin in recgo-tab recgo-browser recgo-desktop recgo-window; do
          cat > $out/share/applications/$tabBin.desktop <<EOF
    [Desktop Entry]
    Type=Application
    Name=$tabBin
    NoDisplay=true
    Exec=$out/bin/$tabBin %f
    X-KDE-DBUS-Restricted-Interfaces=org.kde.KWin.ScreenShot2
    EOF
        done

        for tabBin in recgo-tab recgo-browser recgo-desktop recgo-window; do
          wrapProgram $out/bin/$tabBin \
            --prefix PATH : ${
              lib.makeBinPath (
                [ ffmpeg ]
                ++ lib.optional stdenv.hostPlatform.isLinux chromium
                ++ [ whisper-cpp ]
                ++ lib.optionals stdenv.hostPlatform.isLinux [
                  gst_all_1.gstreamer
                  pulseaudio
                ]
                ++ lib.optional stdenv.hostPlatform.isDarwin switchaudio-osx
              )
            } ${lib.optionalString stdenv.hostPlatform.isLinux ''
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
    description = "A btop-inspired terminal audio recorder (plus recgo-browser, a browser-session recorder)";
    homepage = "https://github.com/eordano/recgo";
    license = licenses.mit;
    maintainers = [ ];
    platforms = platforms.unix;
    mainProgram = "recgo";
  };
}
