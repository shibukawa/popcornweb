# decision:nix-flake-packaging. pw is built from source with buildGoModule; the
# release archives of data:release-artifact serve the Homebrew and manual paths.
{
  lib,
  buildGoModule,
  go,
  src,
  version,
}:
let
  # go.mod requires a Go version a given nixpkgs channel may not ship yet. Fail
  # loudly instead of silently building with an older toolchain; never lower the
  # go directive in go.mod to satisfy a channel.
  directives = lib.filter (line: lib.hasPrefix "go " line) (
    lib.splitString "\n" (builtins.readFile ../go.mod)
  );
  matched = if directives == [ ] then null else builtins.match "go ([0-9.]+)" (lib.head directives);
  required = if matched == null then null else lib.head matched;
in
assert lib.assertMsg (required == null || lib.versionAtLeast go.version required)
  "pw needs Go ${toString required}; this nixpkgs provides ${go.version}. Pin a newer nixpkgs or override the go argument.";
buildGoModule {
  pname = "pw";
  inherit version src;

  # Regenerate whenever go.mod or go.sum changes: set this to lib.fakeHash, run
  # nix build .#pw, and copy the hash the mismatch reports.
  vendorHash = "sha256-M+YlCh5dslEdfcuWPSTzvAWmHl37WRz80fAVMJia0zw=";

  subPackages = [ "cmd/pw" ];

  env.CGO_ENABLED = 0;

  ldflags = [
    "-s"
    "-w"
    "-X github.com/shibukawa/popcornweb/internal/pwcli.version=${version}"
  ];

  # The full suite needs fixtures and toolchains outside the sandbox; the CLI
  # packages are what this derivation ships.
  checkPhase = ''
    runHook preCheck
    go test ./cmd/pw/... ./internal/pwcli/...
    runHook postCheck
  '';

  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    $out/bin/pw help
    $out/bin/pw version | grep -F "pw ${version} "
    runHook postInstallCheck
  '';

  meta = {
    description = "CLI for the Popcorn Web web application framework";
    homepage = "https://github.com/shibukawa/popcornweb";
    mainProgram = "pw";
    license = lib.licenses.asl20;
  };
}
