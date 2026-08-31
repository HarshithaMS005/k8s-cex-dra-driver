# Source nixpkgs from the live nixos-unstable channel so legacy `nix-shell`
# tracks the same fast-moving Go toolchain used in CI. Reproducibility is
# the flake's job (flake.nix pins via flake.lock). Local development
# should always run against the latest unstable.
let
  unstable = builtins.fetchTarball {
    url = "https://github.com/NixOS/nixpkgs/archive/nixos-unstable.tar.gz";
  };
in

{ pkgs ? import unstable {} }:

pkgs.mkShell {
  buildInputs = with pkgs; [
    go
    gopls         # official language server for the Go language
    gotools       # official golang.org/x/tools collection (https://github.com/golang/tools)
    go-tools      # additional tools: https://github.com/dominikh/go-tools
    gnumake
    golangci-lint # required gate (make lint) + advisory profile (make health)
    govulncheck   # required gate (make vulncheck)
    kustomize     # required gate (make manifests-check) - render deploy/kustomize
    shellcheck    # required gate (make bash-check) - lint the repository's own bash
    shfmt         # required gate (make bash-check) - format-check the repository's own bash
    prettier      # required gate (make md-fmt-check) - format-check docs/ markdown
  ];

  # An optional shell may add tools that only its own rules call. Found
  # by testing the tree for it rather than by how the build was invoked,
  # the way the Makefile finds its own optional include. inputsFrom
  # unions that shell's buildInputs into this one, so the list stays in
  # one file and that shell is enterable on its own.
  inputsFrom =
    if builtins.pathExists ./dev/shell.nix
    then [ (import ./dev/shell.nix { inherit pkgs; }) ]
    else [];

  shellHook = ''
    if [[ -t 1 ]]; then
      echo "CEX DRA driver development environment"
      echo ""
      echo "Build:    make"
      echo "Check:    make check"
      echo "Cross:    GOOS=linux GOARCH=s390x make"
      echo "Nix:      nix-build cmd/cex-dra-kubeletplugin/default.nix"
    fi
  '';
}
