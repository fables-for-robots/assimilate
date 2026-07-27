{ lib, buildGoModule }:

buildGoModule {
  pname = "assimilate";
  version = "0.2.4";

  src = lib.cleanSource ./.;

  vendorHash = "sha256-vwgRv5/msjdY4wJCOrD3XpjBHLmidXeb366AuGmZLY8=";

  subPackages = [ "cmd/assimilate" ];

  ldflags = [ "-s" "-w" ];

  meta = {
    description = "assimilate";
    homepage = "https://github.com/jobs-build/assimilate";
    license = lib.licenses.agpl3Only;
    mainProgram = "assimilate";
  };
}
