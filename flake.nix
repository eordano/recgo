{
  description = "recgo - a btop-inspired terminal audio recorder (plus recgo-tab, a browser-tab recorder)";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f (import nixpkgs { inherit system; }));
    in
    {
      packages = forAll (pkgs: rec {
        recgo = pkgs.callPackage ./default.nix { };
        recgo-tab = recgo.overrideAttrs (old: {
          meta = old.meta // {
            mainProgram = "recgo-tab";
          };
        });
        recgo-desktop = recgo.overrideAttrs (old: {
          meta = old.meta // {
            mainProgram = "recgo-desktop";
          };
        });
        default = recgo;
      });
    };
}
