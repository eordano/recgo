{
  lib,
  callPackage,
  python3,
  qt6,
  ffmpeg,
  fontconfig,
  pulseaudio,
  systemd,
  xdg-utils,
  chromium,
  recgo ? callPackage ../. { },
}:

python3.pkgs.buildPythonApplication {
  pname = "recgo-app";
  version = "0.1.0";
  format = "other";

  src = lib.fileset.toSource {
    root = ./.;
    fileset = lib.fileset.unions [
      ./recgo_app
      ./tests
      ./share
    ];
  };

  nativeBuildInputs = [ qt6.wrapQtAppsHook ];

  buildInputs = [
    qt6.qtbase
    qt6.qtwayland
    qt6.qtmultimedia
  ];

  propagatedBuildInputs = [ python3.pkgs.pyside6 ];

  nativeCheckInputs = [ python3.pkgs.pytest ];

  dontWrapQtApps = true;

  installPhase = ''
    runHook preInstall
    site=$out/${python3.sitePackages}
    mkdir -p $site $out/bin $out/share/applications $out/share/icons/hicolor/scalable/apps
    cp -r recgo_app $site/
    cat > $out/bin/recgo-app <<EOF
    #!${python3.interpreter}
    import sys
    from recgo_app.app import main
    sys.exit(main(sys.argv))
    EOF
    chmod +x $out/bin/recgo-app
    cp share/dev.eordano.recgo.desktop $out/share/applications/
    cp share/dev.eordano.recgo.svg $out/share/icons/hicolor/scalable/apps/
    runHook postInstall
  '';

  checkPhase = ''
    runHook preCheck
    export HOME=$TMPDIR XDG_RUNTIME_DIR=$TMPDIR QT_QPA_PLATFORM=offscreen
    export FONTCONFIG_FILE=${fontconfig.out}/etc/fonts/fonts.conf
    export QT_LOGGING_RULES="default.warning=false;qt.multimedia.*=false;qt.core.qobject.connect=false"
    export PYTHONPATH=$PWD:$PYTHONPATH
    pytest -q tests
    python -m recgo_app --shoot $TMPDIR/shots
    test -s $TMPDIR/shots/library.png
    test -s $TMPDIR/shots/hud.png
    runHook postCheck
  '';

  preFixup = ''
    makeWrapperArgs+=(
      "''${qtWrapperArgs[@]}"
      --prefix PATH : ${
        lib.makeBinPath [
          recgo
          ffmpeg
          pulseaudio
          systemd
          xdg-utils
          chromium
        ]
      }
      --set RECGO_HELPERS ${recgo}/bin
      --set RECGO_APP_EXE $out/bin/recgo-app
    )
  '';

  meta = {
    description = "Recgo for KDE: the tray shell over the recgo session recorders";
    homepage = "https://github.com/eordano/recgo";
    license = lib.licenses.mit;
    platforms = lib.platforms.linux;
    mainProgram = "recgo-app";
  };
}
