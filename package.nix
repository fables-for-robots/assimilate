{ lib, buildGoModule }:

buildGoModule {
  pname = "assimilate";
  version = "0.2.1";

  src = lib.cleanSource ./.;

  vendorHash = "sha256-/dPUNlD2x5a0Zj8otONEyvMxDKLrZMG32kwDcitL1dk=";

  subPackages = [ "cmd/assimilate" ];

  ldflags = [ "-s" "-w" ];

  meta = {
    description = "assimilate";
    homepage = "https://github.com/fables-for-robots/assimilate";
    license = lib.licenses.agpl3Only;
    mainProgram = "assimilate";
  };
}
