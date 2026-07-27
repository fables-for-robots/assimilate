{ lib, buildGoModule }:

buildGoModule {
  pname = "assimilate";
  version = "0.2.2";

  src = lib.cleanSource ./.;

  vendorHash = "sha256-xpJ46gBACPMoEzZijmmSsD+nC9t0I9tRicfWTEQP1uQ=";

  subPackages = [ "cmd/assimilate" ];

  ldflags = [ "-s" "-w" ];

  meta = {
    description = "assimilate";
    homepage = "https://github.com/jobs-build/assimilate";
    license = lib.licenses.agpl3Only;
    mainProgram = "assimilate";
  };
}
