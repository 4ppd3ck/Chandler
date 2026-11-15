# Changelog

## 2026-11-14 — awscred renamed to awscreds

- `awscreds` (was `awscred`) — plural, matching what the file holds and
  reading less like a filename. `aw cred` / `aw credentials` follow. Home
  dotfiles deploy on the next option-8 run.
- Note on the helpers: they are commands, not file arguments — run
  `awsconf` bare (vi-opening `awsconf` as a path would just create a
  stray file in the current directory); the raw paths
  (`vi ~/.aws/config`, `vi ~/.aws/credentials`, `vi ~/.kube/config`)
  work the habitual way.

## 2026-11-14 — edit helpers for every auth file: awscred, k8sconf, dispatcher subcommands

- **`awscred`** (`aws_environment`) opens `~/.aws/credentials` — the
  static access/secret-keys side of AWS auth, alongside `awsconf` for the
  SSO side. The two doc comments now spell out the split: SSO sessions
  live in the config (`awsconf`), long-lived keys in the credentials file
  (`awscred`, shadowed by `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` env
  vars, written by `aws configure` and the `aws_profile` picker).
- **`k8sconf`** (`bash_environment`) opens `~/.kube/config`, whatever put
  a cluster there (k8screds merge, eks/aks/oc logins). Its comment flags
  the one trap: k8screds regenerates a merged kubeconfig from
  `~/k8sconfig/*.yaml` on each run, so hand edits to a merged setup
  belong in those YAMLs.
- **Dispatcher integration**: `aw conf` / `aw cred` and `k conf` reach
  the helpers through the same guarded pattern the existing subcommands
  use, and both cheat sheets list the new lines.
- All three dotfiles follow the same editor fallback ($EDITOR → $VISUAL
  → vi) and missing-file guidance. Deployed copies update on the next
  option-8 run; on this machine they were refreshed with timestamped
  backups.

## 2026-11-14 — awsconf: edit the AWS SSO config; the sync now seeds it

- **New `awsconf` helper** (in `aws_environment`, alongside the other AWS
  helpers) opens `~/.aws/config` — the AWS SSO config — in the user's
  editor, falling back through `$EDITOR`, `$VISUAL`, then `vi`, with a
  friendly message when the config does not exist yet.
- **The sync now seeds the config.** The embedded tree ships the SSO
  template as `config.example` (named so the first sync can never deploy
  a placeholder as anyone's live config), while the Bash-tools installer
  links `~/.aws/config` at the deployed `config` — which nothing ever
  created, leaving the symlink dangling on every fresh install (a
  regression from the bash era, whose `custom/` tree shipped the config
  directly). Syncs now create `config` from the template when missing,
  once — afterwards it is the user's file and is never overwritten,
  which is exactly what makes editing it through the symlink safe.
  Verified: `chandler update` seeds it, a user-edited config survives
  further syncs.
- On this machine: config seeded, symlink healed, `awsconf` deployed to
  `~/.aws_environment` (old copy backed up as `~/.aws_environment_bkp_*`).

## 2026-11-14 — Bash-tools install now populates the scripts tree

- **Bug:** the Bash-tools installer (option 8) wired the dotfiles and the
  aliases/dispatchers pointing into `~/.local/share/chandler/scripts`,
  but never populated that tree — on a fresh install every alias, the
  `k`/`aw`/`tf` shortcuts and the `tui_select.sh` picker behind
  `kctx`/`kens`/`k8s_version` were dead until the first `chandler update`
  (observed: `k8s_version` falling back to the numbered prompt because
  the tree was empty). The bashtools action now runs the same
  add-new/overwrite-differing/never-delete sync `update` runs right
  after the installer succeeds, so option 8 is self-contained. A
  sync failure warns without failing the install.
- The already-deployed tree is refreshed by `chandler update` / menu 14 /
  the `u` key as before.

## 2026-11-13 — aws installer fixed on macOS (the universal-bundle URL never existed)

- `install-aws.sh`'s macOS branch downloaded
  `awscli-exe-macos-universal.zip` — a URL that 404s (AWS publishes no
  macOS exe bundle at all; every `awscli-exe-macos-*` variant is 404).
  The branch now installs via Homebrew (`brew install awscli`, no sudo,
  the same channel the SSM plugin already used), falling back to the
  official `AWSCLIV2.pkg` through `sudo installer` when brew is absent.
  Verified end to end: `chandler tool aws` installs AWS CLI v2.40.2 and
  `doctor` reports it. The Session Manager plugin's cask needs a sudo
  password — it prompts in a real terminal and warns cleanly where none
  exists; rerun `chandler tool aws` from a terminal to add it.

## 2026-11-13 — installed-tools summary in the menu

- The menu now answers "what was already installed" at a glance:
  installer-backed entries carry a dim `· installed` marker and the status
  line counts them (`✓ prerequisites ready · 6/10 tools installed`). The
  probe covers all ten installer actions — `exec.LookPath` for the PATH
  tools, the AppImage files in `~/.local/bin` for the browsers, and the
  deployed scripts tree for the Bash tools — and refreshes after every
  action, so markers track reality through the session (install a tool,
  its marker appears on the next draw; the bulk runs update them per
  tool). The fallback numeric menu prints the same facts as `(installed)`
  tags. `chandler doctor` remains the detailed, versioned report.
- Tests pin the probe contract (all ten installer actions probed, nothing
  else), the rendered summary (count line, marker count, nil-map case),
  and the in-place refresh semantics.

## 2026-11-13 — one darwin package: chandler-darwin-<arch>.zip

- The darwin CLI package is gone — it carried the same binary, icon and
  README the app bundle carries, so `chandler-darwin-<arch>-app.zip` and
  `chandler-darwin-<arch>.zip` were one payload twice. The app bundle is
  the survivor, renamed to `chandler-darwin-<arch>.zip`; CLI use runs the
  binary directly from `Chandler.app/Contents/Resources/chandler` (the
  bundle README says so). The release set is now nine zips + SHA256SUMS:
  one darwin zip per arch, one zip per linux/freebsd/windows target.
  Also fixed while touching the packaging: the bundle's .icns now gets
  its 0644 mode (the chmod had landed in the CLI package's stage instead).

## 2026-11-13 — release trimmed to 11 packages (no bare binaries)

- The nine bare per-platform binaries are gone from `release/`: every
  package already carries its platform's binary (the darwin CLI zip, the
  linux/freebsd desktop bundles, the windows zip), so shipping the binary
  twice was pure duplication. The set is now 11 zips + `SHA256SUMS` —
  two artifacts per darwin arch (app bundle + CLI package), one zip each
  for linux (×4), freebsd and windows (×2). The release script builds
  each binary into a temp dir and sweeps it once its package is written;
  the README table follows. Inside the artifacts, only the app-bundle
  README's pointer to the (now absent) bare binary changed.

## 2026-11-13 — build and release scripts moved to the repo root

- `scripts/release.sh`, `scripts/release.bat`, `scripts/build.sh` and
  `scripts/build.bat` now sit at the checkout root and the `scripts/`
  folder is gone — the same flat layout the Appdeck project uses
  (`./build.sh` for the dev binary, `./release.sh` for the artifact set).
  The build scripts' `cd` paths follow the new home (the root itself, not
  its parent); `release.sh`'s go.mod walk-up makes its location
  indifferent. README and the doc comments follow.

## 2026-11-13 — zip-only packages, FreeBSD and linux/riscv64 targets

- **One archive format.** Every release package is now a zip — the darwin
  CLI package (`chandler-darwin-<arch>.zip`, was `.tar.gz`) and the Linux
  desktop bundle (`chandler-linux-<arch>.zip`, was `.tar.gz`) joined the
  app bundles and Windows zips. The unix-content zips require Info-ZIP
  `zip` (it preserves the exec bits the tarballs carried; PowerShell's
  Compress-Archive would strip them); the Windows zip keeps its
  PowerShell fallback. Verified end to end: tar modes carry into the zip,
  `install.sh` extracts executable.
- **FreeBSD (amd64) and linux/riscv64 are release targets** (the
  Appdeck matrix's reasoning: NAS/homelab boxes; RISC-V SBCs and QEMU —
  both pure CGO_ENABLED=0 cross-compiles). FreeBSD ships the same desktop
  bundle as Linux; prerequisite installs now go through `pkg`
  (`detectOS` maps freebsd, `pkgmgr` probes and runs it). Upstream asset
  reality is documented per tool: on FreeBSD terraform/k9s/fzf install,
  kubectl/helm have no FreeBSD builds; on riscv64 only helm/fzf do.
- **Installer fixes found while checking those assets:** k9s's OS mapping
  capitalizes the platform (`k9s_Freebsd_amd64.tar.gz` — lowercase 404'd),
  and fzf's checksum file is `fzf_<version>_checksums.txt` with
  underscores, not dashes — the dash URL would have failed the new
    checksum verification on every fzf install.

## 2026-11-13 — release.sh as a self-contained pipeline (Appdeck-style)

- `release.sh` no longer delegates to a Go packager tool: it is a
  single POSIX-sh script — like the sibling Appdeck project's release
  script — that walks up to the go.mod directory for the app name,
  cross-compiles every target (stripped, static, `-trimpath`, version
  stamped `git-<rev>` or `build-<timestamp>`), packages it per platform
  (darwin .app bundle + CLI tarball with an ad-hoc codesign where
  `codesign` exists, linux tarball with desktop entry/install.sh/hicolor
  icons, windows zip with a PowerShell `Compress-Archive` fallback on
  Git Bash hosts), and writes a sorted, byte-stable `SHA256SUMS`
  (sha256sum, falling back to `shasum -a 256` on macOS).
- `tools/packager` is gone — the artifact set is identical to what it
  produced (verified: all 17 artifacts + SHA256SUMS, bundle structure,
  tar modes, placeholder-free rendered files, and the stamped binary
  reporting `chandler build-…`); `release.bat` is the Appdeck-style
  `sh` shim.
- Target filters are unchanged (`./release.sh linux`,
  `./release.sh darwin/arm64`); unknown targets are rejected with
  the full list, and an invalid target aborts before anything is built.

## 2026-11-13 — multi-select, tool pass-through, doctor --json, windows resize

- **Menu multi-select.** Space checks installer-backed entries (✓ column in
  the menu; the bulk/config/update/uninstall entries are not checkable),
  and ⏎ with checks runs them in menu order through the same
  confirm-per-tool-logs-✓/✗-summary path as the bulk actions — a
  hand-picked set now behaves exactly like "Install everything". New
  `runToolList` in the runner is the one path every multi-tool run takes.
- **`chandler tool` argument pass-through.** Everything after `--` reaches
  the installer script verbatim (`chandler tool kubectl -- 1.31.0`); the
  four version-prompting installers (kubectl, helm, k9s, terraform) now
  accept a version as their first argument and only prompt when none was
  passed, so unattended runs are complete. Extra arguments are rejected
  for the bulk actions.
- **`chandler doctor --json`.** The doctor's facts are probed into a
  report struct first, then rendered as text or JSON (`--json`/`-j`) —
  identical facts either way, machine-readable for scripts.
- **Windows resize watcher.** Windows has no SIGWINCH; a console resize
  arrives as a WINDOW_BUFFER_SIZE_EVENT record in the input buffer. A
  stdlib-syscall watcher waits on the input handle and consumes only
  resize records — a key record at the front of the queue is left for the
  keystroke reader or the child installers' prompts, so it can never eat
  a keystroke. Non-console stdin (mintty pipes) silently disables the
  watcher; the next-keypress fallback remains everywhere.
- First `main` test for the tool argument grammar, and a menu test pinning
  the checkable rule.

## 2026-11-13 — TODO.md implementation pass: checksums, doctor/tool/update-check, logs, tests, split

Everything tracked in TODO.md's issues, recommendations and feature ideas
is implemented; the resolved list there records the detail.

- **Downloads are checksum-verified.** kubectl (dl.k8s.io `.sha256`), helm
  (get.helm.sh `.sha256sum`), k9s (release `checksums.sha256`), terraform
  (HashiCorp `SHA256SUMS`) and fzf (release checksums file) verify against
  their official checksum before anything moves into `~/.local/bin` — a
  missing or mismatching checksum refuses the install. oc verifies when
  the mirror publishes a checksum and warns otherwise; AWS CLI, SSM
  plugin, the browser AppImages and kubectx/kubens publish none, and their
  installers now say so explicitly.
- **New subcommands.** `chandler doctor` prints a read-only status report
  (root, pin, config.env, PATH and shell-config blocks, prerequisites,
  installed tools with versions, deployed scripts, installed command).
  `chandler tool <name>` installs one menu entry non-interactively
  (`-y` skips the bulk confirm, `chandler tool` lists the names).
  `chandler update-check` compares the build against the release feed
  configured at publish time (or `CHANDLER_RELEASES_API` for one run);
  explicit-only, exit 2 when a newer release exists.
- **Installer output is logged.** Every action tees its child output into
  `<root>/tmp/logs/<tool>-<timestamp>.log` and failures print the path;
  the bulk runs finish with a ✓/✗ summary table and per-tool elapsed time.
- **main.go is split.** Root resolution + pin → `internal/rootpin`, PATH/
  shell-config setup → `internal/pathsetup`, the installed-command
  keep-alive and atomic self-refresh → `internal/selfinstall`, the
  removal → `internal/uninstall`; main.go is startup flow and subcommands.
- **TUI QoL.** A SIGWINCH watcher redraws on resize (windows: next
  keypress, as before); the highlighted entry is remembered across runs
  in `~/.config/chandler/menu`.
- **Fixes.** The jq bootstrap download got a 5-minute HTTP client timeout;
  the selected menu row's description resets its color; the startup
  self-check compares sizes before reading whole binaries.
- **CI removed; the sealing key stays offline.** `.github/workflows/ci.yml`
  (vet, test, build, shell syntax, and a release-artifact-set check via
  the packager) is gone — those checks run locally instead (`go vet` /
  `go test`, `bash -n`, a `release.sh` run). The authorship claim keeps
  shipping as the sealed `claim.bin`; the 32-byte key and the small
  sealing tool stay out of the repo (`key.bin` is gitignored), so
  re-sealing remains an offline ceremony.
- **Test suite.** `go test ./...` now covers config parse/save/load,
  the root chain and the nested-marker trap, the keystroke decoder
  (escape bursts, the split-sequence window, parked bytes), customsync's
  sync rules and legacy migration, EnsureCopy's install/refresh/skip
  contract, the shell-config append/strip family, and the 15-entry menu
  contract.

## 2026-11-13 — zsh support, uninstall menu entry

- **zsh is wired up end to end.** The Bash-tools installer (menu option 8)
  now appends a marker-guarded `# >>> chandler shell tools >>>` block to
  `~/.zshrc` that sources the same four dotfiles, so the aliases and the
  `k` / `aw` / `tf` dispatchers work in zsh too; the user's own `.zshrc`
  is never replaced, only appended to, and the block is idempotent across
  re-runs. The dotfiles themselves are now zsh-compatible: the bash-only
  `shopt -s expand_aliases` is guarded with `$BASH_VERSION`, and the
  `tf unlock` prompt uses printf+read instead of bash's `read -p` (zsh's
  `-p` reads from the coprocess).
- **fzf + cursor setup works under zsh.** `install-fzf-setup.sh` writes
  its opt-in block to `~/.zshrc` as well as `~/.bashrc` (`.zshrc` when it
  exists or zsh is the login shell). `fzf_setup.sh` grows a zsh branch:
  Ctrl+R / Ctrl+T / Alt+C come from fzf's own zsh integration
  (`fzf --zsh`), Alt+S is a zle widget over the same combined
  aliases/functions/history/executables picker. `set_cursor.sh` and
  `fzf_setup.sh` detect "sourced vs executed" correctly under zsh
  (`ZSH_ARGZERO` vs `$0` — `BASH_SOURCE` does not exist there), so they
  stay silent when sourced at shell startup.
- **New menu entry 15 — "Uninstall chandler"** (group `danger`, red in
  the TUI). It confirms, then runs the same `uninstall` subcommand as a
  subprocess of the running binary, and a successful uninstall ends the
  menu session instead of returning to a menu the app just removed
  itself from. A failed uninstall keeps the menu, like any failed action.
- **`chandler uninstall` strips every chandler marker-block kind** from
  the shell configs: the strip step now matches the whole
  `# >>> chandler …` family by prefix/suffix (the PATH block, the new
  `shell tools` block, and the `fzf/cursor setup` block — which the
  line-exact match previously left behind).
- **A zsh login shell with no shell config bootstraps `~/.zshrc`**
  instead of exiting with "No compatible shell config file found." — a
  fresh macOS machine (zsh default since Catalina) usually ships without
  one. The bash-shell behavior is unchanged.
- The PATH block no longer glues onto a config's last line that lacks a
  trailing newline (the begin marker now always starts on a fresh line —
  a glued marker stopped uninstall's strip from matching).
- Stale menu description fixed: entry 14 says `~/.local/share/chandler`,
  not the pre-data-root `~/.local/bin`.
- First Go tests: `main_test.go` covers the marker-family strip, the
  append newline-glue case and append idempotence;
  `internal/menu/menu_test.go` pins the 15-entry contract and the
  uninstall entry.

## 2026-09-26 — release artifacts: macOS tarballs, Windows zips

- The macOS bundle is now `chandler-darwin-<arch>-app.zip` (it was
  `.app.zip`). Each darwin target also ships a `chandler-darwin-<arch>.tar.gz`
  CLI package (the bare binary, `chandler.icns` and a README), and each
  windows target now ships a `chandler-windows-<arch>.zip` next to its `.exe`
  (the binary, `chandler.ico` and a README). `release/SHA256SUMS` covers the
  new artifacts, and the packager's clean step sweeps the old names. The
  CI artifact-set check and the release wrappers' docs follow the new
  names too.

## 2026-09-26 — remaining pre-rename paths moved to chandler

- The last pre-rename paths are now branded `chandler`: the root-pin file
  (`~/.config/chandler/path`), the last-resort deployment root
  (`$HOME/chandler`), the installer fallbacks for the custom-scripts tree and
  `tmp/`, and the fzf/cursor marker block written to `~/.bashrc`. The strip
  step matches the marker suffix as well as the exact line, so marker blocks
  left behind by the older names are cleaned up on the next run too.

## 2026-09-26 — ship-chandler banner

- The interactive TUI banner is now a six-line sailing ship riding waves
  with the `C H A N D L E R` wordmark centered under the hull, and the
  compact (short-terminal) header uses a ship glyph (🚢): the menu is
  branded after the app's role as a ship chandler — supplying docked
  ships and their crews with provisions, spare parts and operational
  equipment.

## 2026-09-24 — chandler rebrand, packaged apps, icon

- The project is now **chandler**: the Go module, the CLI command
  (`chandler` / `chandler.exe`, installed to `~/.local/bin/chandler`), the
  TUI banner and wordmark, the release artifacts and the docs all use the
  new name. The `DEVOPS_*` environment variables were renamed to
  `CHANDLER_*` (`CHANDLER_SELF`, `CHANDLER_BASH`, `CHANDLER_ROOT`,
  `CHANDLER_ROOT_PIN`, `CHANDLER_CUSTOM_SCRIPTS`, `CHANDLER_K8S_STACKS`,
  `CHANDLER_CLOUD_PROVIDERS`) and the root pin moved to
  `~/.config/chandler/path`; `config.env` uses the new keys. The one
  deliberately kept legacy path is the bash-era checkout location, which
  names where a checkout lives, not the app. On this machine the pin file
  and `config.env` were migrated.
- Release packaging moved into `tools/packager`, driven by
  `build-release.sh` and the new `build-release.ps1` (Windows). Besides the
  cross-compiled binaries it now emits a macOS `.app` bundle per darwin
  target (`chandler-darwin-<arch>.app.zip`) and a Linux tarball per linux
  target (`chandler-linux-<arch>.tar.gz`: binary + `.desktop` + hicolor
  icons + per-user installer), and it sweeps the pre-rename `devops-*`
  artifacts out of `release/`.
- New `icons/` set — master PNG, `.ico`, `.icns` and the Linux hicolor PNGs
  — generated by the committed `icons/generate_icons.py`. Windows binaries
  embed the icon through committed `rsrc_windows_*.syso` resource objects,
  so a plain `go build` produces an `.exe` that already carries it.

## 2026-09-24 — platform hardening pass

- LF line endings across the repo, enforced by `.gitattributes`: the bash
  scripts and dotfiles were CRLF, which Git Bash tolerates but macOS/Linux
  chokes on (`$'\r': command not found`, PATH lines poisoned with `\r`).
- Deployment-root resolution understands both checkout layouts — module at
  the checkout root and module nested in `GO/`. A flat checkout previously
  never matched the marker and fell through to the bash-era default
  checkout, so `config.env` and `tmp/` landed in a phantom directory and
  `update` could not find the checkout binary.
- Windows: refreshing the installed command now works while that copy is
  running (rename-aside; a running image cannot be replaced in place).
- jq bootstrap pins jq 1.8.2 and verifies the SHA256 from the release's
  official `sha256sum.txt` before installing.
- `chandler version` + version stamping in `build-release.sh` (git revision
  or timestamp).
- Stale `chandler-assets-*` temp dirs (from crashed sessions) are swept at
  startup; `install` also sets up the PATH; the TUI re-measures the
  terminal per keypress and repaints after a resize.
- Test suite: keystroke decoding, root resolution (both layouts, module-dir
  disambiguation), config.env parse/roundtrip, customsync, home resolution,
  PATH setup (both platforms), bash resolution, stale-file sweep. CI
  workflow: tests on linux+windows, gofmt, script syntax, release build.

## 2026-09-23 — Windows support

- `internal/term` restructured per OS; windows implementations via the
  Console API (raw mode with virtual-terminal input, console-mode tty
  probe, `GetConsoleScreenBufferInfo` sizing, VT output enabled for
  classic conhost). BSD-family termios files widened from darwin.
- TTY probes and raw-mode calls use `os.Stdin/os.Stdout .Fd()` instead of
  literal fd 0/1 — handle 0 is not stdin on windows, so the interactive
  TUI could never appear.
- Prerequisite failure warns and continues to the menu instead of exiting
  (the instant-close on a fresh windows box).
- Git Bash is located automatically (git.exe sibling walk, known install
  roots, `CHANDLER_BASH` override) — plain `bash` resolves to the System32
  WSL launcher, the source of `execvpe(/bin/bash) failed` errors.
- Installers handle Windows: kubectl, terraform, helm, k9s, oc, fzf and
  AWS CLI (MSI) install their windows builds; k9s' capitalized asset
  scheme fixed unix too. jq is direct-download bootstrapped; the windows
  prerequisite list shrinks to jq alone.
- PATH setup writes `~/.bashrc`/`.bash_profile` and both PowerShell
  profiles, content-guarded against duplicates; `$HOME` falls back to
  `%USERPROFILE%`; PATH joins with the platform separator.
- `build-release.sh` cross-compiles stripped release binaries for
  darwin/linux/windows (amd64 + arm/arm64) into `release/` with SHA256SUMS.

## Earlier — the bash-to-Go port (increments 1–14)

1. Deployment-root resolution and OS detection.
2. Package-manager detection, installation, prerequisite checks.
3. `config.env` loading and the stack/cloud notes.
4. LOCAL_BIN PATH block and the tmp directory.
5. Terminal helpers (`internal/term`).
6. Stack/cloud questions (`ask.AskStackCloud`) before the menu.
7. Menu entries and the action runner (`internal/menu`).
8. Menu phase wired into main — TUI/fallback dispatch.
9. Empty-Enter defaults; root resolution "from anywhere"; custom-scripts
   sync in the update action.
10. Subcommands: `update`, `help`/`-h`/`--help`, argument rejection.
11. Portable `chandler` copy on the PATH; root pin file.
12. Self-contained binary: assets embedded at build time; per-run
    materialization; pure-Go update.
13. `ensureCopy` semantics — a deleted `chandler` command is re-installed.
14. Startup ensure keeps the installed command current at every launch.

## 2026-09-24 — cleanup

- The authorship kit moved from ~/.claim into a gitignored `claim/`
  folder inside the project (keys, signing keypair, allowed-signers, the
  seal ceremony) with its own README; the sealed ciphertext stays in
  `internal/claim/claim.bin` — public by design, safe to push.
- Test files removed at the project owner's direction; the suite is
  recoverable from git history (`git restore --source=59711d6 -- '*_test.go'`).
