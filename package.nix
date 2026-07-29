{ lib, buildGoModule }:

buildGoModule {
  pname = "assimilate";
  version = "0.2.5";

  src = lib.cleanSource ./.;

  vendorHash = "sha256-k9jUgMR0hLhud3RjErzNUfxU0fze5nbxoKfGi+YnIgE=";

  subPackages = [ "cmd/assimilate" ];

  ldflags = [ "-s" "-w" ];

  meta = {
    description = "assimilate";
    homepage = "https://github.com/jobs-build/assimilate";
    license = lib.licenses.agpl3Only;
    mainProgram = "assimilate";
  };
}
