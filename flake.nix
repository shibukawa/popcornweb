{
  description = "pw, the CLI of the Popcorn Web web application framework";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      # requirement:cli-distribution. Bumped in the release preparation commit
      # that precedes the tag; see flow:cli-release.
      baseVersion = "0.0.0";

      version = if self ? rev then baseVersion else "${baseVersion}-${self.dirtyShortRev or "dirty"}";

      # x86_64-darwin is absent because nixpkgs 26.11 dropped it. Intel macOS is
      # still served by the Homebrew formula and the release archive of
      # data:release-artifact.
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];

      forEachSystem = nixpkgs.lib.genAttrs systems;
      pkgsFor = system: nixpkgs.legacyPackages.${system};

      # The Go series go.mod asks for. nixpkgs' default go trails it by a
      # release, so naming the series here keeps the assertion in nix/pw.nix for
      # a channel that has not shipped it, instead of for every channel whose
      # default has not caught up.
      goFor = pkgs: {
        go = pkgs.go_1_27;
        buildGoModule = pkgs.buildGo127Module;
      };
    in
    {
      overlays.default = final: _prev: {
        pw = final.callPackage ./nix/pw.nix (
          {
            src = self;
            inherit version;
          }
          // goFor final
        );
      };

      packages = forEachSystem (
        system:
        let
          pkgs = pkgsFor system;
          pw = pkgs.callPackage ./nix/pw.nix (
            {
              src = self;
              inherit version;
            }
            // goFor pkgs
          );
        in
        {
          inherit pw;
          default = pw;
        }
      );

      apps = forEachSystem (system: rec {
        pw = {
          type = "app";
          program = "${self.packages.${system}.pw}/bin/pw";
          meta = { inherit (self.packages.${system}.pw.meta) description; };
        };
        default = pw;
      });

      devShells = forEachSystem (
        system:
        let
          pkgs = pkgsFor system;
          goToolchain = goFor pkgs;
        in
        {
          default = pkgs.mkShell {
            # Host tools of decision:host-tools-target-runtime. Tailwind stays
            # with Devbox per decision:tailwind-host-toolchain. gopls and
            # tinygo already build against the newest Go in nixpkgs; gotools
            # takes the default one, so it is told which Go to use, or
            # goimports would parse with a go/parser older than the source it
            # formats.
            packages = [
              goToolchain.go
              pkgs.gopls
              (pkgs.gotools.override goToolchain)
              pkgs.tinygo
            ];
          };
        }
      );

      formatter = forEachSystem (system: (pkgsFor system).nixfmt);
    };
}
