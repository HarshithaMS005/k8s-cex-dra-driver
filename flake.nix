{
  description = "CEX DRA driver for Kubernetes - kubelet plugin";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "s390x-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system:
        f system (import nixpkgs { inherit system; }));
    in
    {
      packages = forAllSystems (system: pkgs: {
        cex-dra-kubeletplugin = import ./cmd/cex-dra-kubeletplugin/default.nix {
          inherit pkgs;
        };
        default = self.packages.${system}.cex-dra-kubeletplugin;
      });

      checks = forAllSystems (system: pkgs: import ./checks.nix { inherit pkgs; src = ./.; });

      # Take the shell from shell.nix rather than restating its package
      # list, so `nix develop` and `nix-shell` cannot drift apart.
      devShells = forAllSystems (system: pkgs: {
        default = import ./shell.nix { inherit pkgs; };
      });
    };
}
