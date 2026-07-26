{ lib, buildGoModule }:

buildGoModule {
  pname = "assimilate";
  version = "0.2.2";

  src = lib.cleanSource ./.;

  vendorHash = "sha256-jeJIH0ZeCqR5/kl0vKkSwwVsCEcpH89E84Je3PQI6xc=";

  subPackages = [ "cmd/assimilate" ];

  ldflags = [ "-s" "-w" ];

  meta = {
    description = "assimilate";
    homepage = "https://github.com/fables-for-robots/assimilate";
    license = lib.licenses.agpl3Only;
    mainProgram = "assimilate";
  };
}
