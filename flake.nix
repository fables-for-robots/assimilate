{
  description = "assimilate";
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";

    systems.url = "github:nix-systems/default";

  };

  outputs = { self, nixpkgs, systems, ... }@inputs:
    let
      eachSystem = f:
        nixpkgs.lib.genAttrs (import systems)
        (system: f system nixpkgs.legacyPackages.${system});
    in {

      overlays.default = final: prev: {
        assimilate = final.callPackage ./package.nix { };
      };

      packages = eachSystem (system: pkgs: rec {
        assimilate = pkgs.callPackage ./package.nix { };
        default = assimilate;
      });

      apps = eachSystem (system: pkgs: rec {
        assimilate = {
          type = "app";
          program = "${self.packages.${system}.assimilate}/bin/assimilate";
          meta = { inherit (self.packages.${system}.assimilate.meta) description; };
        };
        default = assimilate;
      });

      devShells = eachSystem (system: pkgs: {
        default = pkgs.mkShell {
          shellHook = ''
            # Set here the env vars you want to be available in the shell
          '';
          hardeningDisable = [ "all" ];

          packages = with pkgs; [ go gh ];
        };
      });
    };
}
