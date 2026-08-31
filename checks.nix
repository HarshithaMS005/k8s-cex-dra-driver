# Default to live nixos-unstable for legacy `nix-build checks.nix`. When
# invoked via the flake (`nix flake check`), `pkgs` is passed in and
# pinned via flake.lock - that's the reproducible path.
#
# `vendor/` is committed, so the git-tracked source
# the flake sees carries the full Go dependency tree and module resolution
# works offline in the build sandbox. `nix flake check` and
# `nix-build checks.nix -A all` are equally valid gates.
let
  unstable = builtins.fetchTarball {
    url = "https://github.com/NixOS/nixpkgs/archive/nixos-unstable.tar.gz";
  };
in

{ pkgs ? import unstable {}
, src ? ./.
}:

let
  makeCheck = target: pkgs.stdenv.mkDerivation {
    name = "cex-dra-driver-${target}";
    inherit src;
    nativeBuildInputs = with pkgs; [
      go gnumake golangci-lint gotools
    ];
    buildPhase = ''
      export HOME=$TMPDIR
      export GOCACHE=$TMPDIR/go-cache
      export GOLANGCI_LINT_CACHE=$TMPDIR/golangci-cache
      # The go.mod toolchain pin targets network builds (dev shell, image
      # build). The sandbox cannot download toolchains, so run nixpkgs go.
      # The go directive is the floor that still guards compatibility here.
      export GOTOOLCHAIN=local
      make ${target}
    '';
    installPhase = "mkdir -p $out";
  };

  # govulncheck is intentionally NOT wired into the flake check gate:
  # it fetches the vuln database from vuln.go.dev on every run, which
  # the nix build sandbox blocks. `make vulncheck` is the authoritative
  # entry point - run it from a normal shell or a CI step with network.
  fmt-check = makeCheck "fmt-check";
  lint      = makeCheck "lint";
  test      = makeCheck "test";

  # Render the Kustomize deployment to catch malformed manifests, bad
  # references, and broken patches. Pure/offline (local files only), so it runs
  # under the flake-check sandbox once the manifests are git-tracked. Delegates
  # to `make manifests-check` so the overlay list lives in one place (the
  # Makefile). The target only shells out to kustomize, so it stays pure. Unlike
  # makeCheck this needs kustomize, not the Go toolchain, hence its own inputs.
  manifests-check = pkgs.stdenv.mkDerivation {
    name = "cex-dra-driver-manifests-check";
    inherit src;
    nativeBuildInputs = with pkgs; [ kustomize gnumake ];
    buildPhase = "make manifests-check";
    installPhase = "mkdir -p $out";
  };

  # Lint the repository's own bash (syntax, shellcheck, shfmt). Pure/offline
  # (local files only), so it runs under the flake-check sandbox. Delegates to
  # the make target so the file list and tool flags live in one place (the
  # Makefile), which names each file rather than sweeping a directory, so
  # vendored references/** scripts are never linted.
  bash-check = pkgs.stdenv.mkDerivation {
    name = "cex-dra-driver-bash-check";
    inherit src;
    nativeBuildInputs = with pkgs; [ bash shellcheck shfmt findutils gnumake ];
    buildPhase = "make bash-check";
    installPhase = "mkdir -p $out";
  };

  # Format-check the public docs markdown (docs/). Pure/offline (prettier
  # reads local files only), so it runs under the flake-check sandbox.
  # Delegates to the make target so the path glob and config
  # (.prettierrc.yaml) live in one place.
  md-fmt-check = pkgs.stdenv.mkDerivation {
    name = "cex-dra-driver-md-fmt-check";
    inherit src;
    nativeBuildInputs = with pkgs; [ prettier gnumake ];
    buildPhase = "make md-fmt-check";
    installPhase = "mkdir -p $out";
  };

  # Every check that runs with nothing but this tree.
  baseChecks = {
    inherit fmt-check lint test manifests-check bash-check md-fmt-check;
  };

  # An optional file may add checks over inputs this tree does not always
  # carry. Found by testing the tree for it rather than by how the build
  # was invoked, the way the Makefile finds its own optional include.
  extraChecks =
    if builtins.pathExists ./dev/checks.nix
    then import ./dev/checks.nix { inherit pkgs src; }
    else {};

  checks = baseChecks // extraChecks;
in
checks // {
  all = pkgs.symlinkJoin {
    name  = "cex-dra-driver-checks-all";
    paths = builtins.attrValues checks;
  };
}
