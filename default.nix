{ buildGoModule, config, lib, pkgs, installShellFiles, date, commit }:

let pkg = "github.com/f1bonacc1/process-compose/src/config";
in
(buildGoModule.override { go = pkgs.go_1_26; }) rec {
  pname = "process-compose";
  version = "1.120.0+gkze1";
  env.CGO_ENABLED = 0;

  src = lib.cleanSource ./.;
  ldflags = [
    "-X ${pkg}.Version=v${version}"
    "-X ${pkg}.Date=${date}"
    "-X ${pkg}.Commit=${commit}"
    "-X ${pkg}.CheckForUpdates=false"
    "-X ${pkg}.SelfUpdateEnabled=false"
    "-s"
    "-w"
  ];

  nativeBuildInputs = [ installShellFiles ];
  nativeCheckInputs = [ pkgs.gitMinimal ];

  vendorHash = "sha256-IY43JoWNM8HMz8H+X80IRQ1vWjsqceWHgb/BkxzGyK0=";

  postInstall = ''

    installShellCompletion --cmd process-compose \
      --bash <($out/bin/process-compose completion bash) \
      --zsh <($out/bin/process-compose completion zsh) \
      --fish <($out/bin/process-compose completion fish)
  '';

  meta = with lib; {
    description = "A simple and flexible scheduler and orchestrator to manage non-containerized applications";
    homepage = "https://github.com/gkze/process-compose";
    changelog = "https://github.com/gkze/process-compose/releases/tag/v${version}";
    license = licenses.asl20;
    mainProgram = "process-compose";
  };

  doCheck = true;
}
