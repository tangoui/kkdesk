# kkdesk Release Installers Implementation Plan

> Execute the tasks in order using superpowers:executing-plans. Verify each change before its separate commit.

**Goal.** Ship drag-to-install disk images, a Windows per-user installer, and native Linux packages alongside the existing signed update assets.

**Architecture.** Build each platform once. Package the resulting binaries and bundles on their original runners. A reusable workflow feeds both tag releases and a branch-only artifact check. The desktop app selects an update target appropriate to how it was installed.

**Tools.** Go, Tango CLI v0.4.0, GitHub Actions, hdiutil, NSIS, appimagetool.

**Spec.** [Release installers design](../specs/2026-11-27-installers-design.md).

## Global constraints

- Work on `feat/installers` in `.kkdesk-installers`. Push only that branch for CI. Preserve the tag-triggered publication path and existing repository keys.
- Keep the manifest formats `app`, `exe` and `dir`, including the current asset names and support for patches.
- Use Windows per-user installation under `%LOCALAPPDATA%\Programs\kkdesk`. Keep the privileged component in Program Files and preserve `%APPDATA%\kkdesk` during uninstall.
- Run local package checks using unprivileged builds and read-only DMG mounts.
- End each commit message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Task 1 — Desktop update targets

Files include `main.go`, `installed_relaunch_windows.go`, `installed_relaunch_other.go`, and `internal/update`.

- [ ] Add failing tests for protected Windows images with and without a registered per-user copy, regular Windows exe updates, Linux AppImage/deb versus directory updates, and an existing platform control case.
- [ ] Run `go test -count=1 ./internal/update` and verify the missing behavior.
- [ ] Configure Tango's `Updater.Target` when the protected Windows process can locate the current user's installer registration. Let a normally started stale Program Files copy hand off to the registered user executable. Keep the existing matching-image test and handoff loop guard for switching to Program Files.
- [ ] Disable the in-app updater from AppImage and the Debian system executable while the Linux manifest delivers a directory archive.
- [ ] Run the focused tests and Windows vet/link. Commit as `fix(update): keep desktop installer updates out of managed paths`.

## Task 2 — Shared desktop packaging

Files include `tan.json`, `scripts/package-macos.sh`, `scripts/windows-installer.nsh`, `.github/workflows/package-desktop.yml`, `.github/workflows/check-installers.yml`, and `.github/workflows/release.yml`.

- [ ] Install the published Tango CLI to a temporary directory and confirm its package/manifest options. Wire the tag release and branch-only check to the same desktop workflow.
- [ ] Build both Mac bundles, add the existing launchd plists, sign and verify, then create and sign a DMG per bundle. Mount each image read-only and verify the Applications symlink, embedded plists, and app signature.
- [ ] Run the Windows `tan package` build with the existing mingw-w64 flags and per-user NSIS configuration. Extend uninstall with optional elevated removal through the protected kkdesk executable and clear feedback for skip or failure. Keep the raw exe from the same build.
- [ ] On native Linux x64 and arm64 runners, make AppImage and deb in one `tan package` call. Check the executable bytes inside each installer against the raw build and keep the raw Linux build tree for the existing update archive.
- [ ] Make the branch check assemble archives and sign a test manifest using a temporary key. Assert the five existing platform formats and asset names. Upload only artifacts.
- [ ] Run local Mac and Windows package checks, syntax checks, and `git diff --check`. Commit as `build(release): produce desktop installers alongside update assets`.

## Task 3 — Installation, update and release documentation

Files include `docs/guide/install.md`, `docs/guide/update.md`, `docs/releases/0.1.0.md`, `README.md`, `README.zh-CN.md`, and `README.ja.md`.

- [ ] Document DMG drag-and-drop and Gatekeeper, Windows per-user install and the lock-screen component lifecycle, and Linux AppImage and deb installation and updates. Add the package types to the next release notes.
- [ ] Review download links and asset naming across all three READMEs. Run `git diff --check` and commit as `docs: explain desktop installation and updates`.

## Branch verification

- [ ] Run `gofmt -l .`, `go vet ./...`, `go test -count=1 ./...`, Linux vet with `CGO_ENABLED=0`, and Windows amd64 vet and link with mingw-w64.
- [ ] Push `HEAD:refs/heads/feat/installers`. Wait for CI and the packaging check on the pushed commit, inspect uploaded asset names, and provide both run URLs.
- [ ] Include Windows runtime steps for installation, shortcuts, the matching-copy handoff, auto-update, component update and both uninstall choices.
