{ lib, buildGoModule }:

buildGoModule {
  pname = "assimilate";
  version = "0.2.3";

  src = lib.cleanSource ./.;

  vendorHash = "sha256-XSwTYbQMb+LtGT7xetw5p6EE3btcJRBF1xQoT3EtVRc=";

  subPackages = [ "cmd/assimilate" ];

  ldflags = [ "-s" "-w" ];

  meta = {
    description = "assimilate";
    homepage = "https://github.com/jobs-build/assimilate";
    license = lib.licenses.agpl3Only;
    mainProgram = "assimilate";
  };
}
