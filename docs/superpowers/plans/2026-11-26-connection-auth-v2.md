# Connection Authentication v2 Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan task by task. Steps use checkboxes for tracking.

**Goal** — Authenticate the host and verify passwords over DTLS for desktop LAN and cross-network connections and phone LAN connections. Recover a phone pairing when the final receipt was lost.

**Architecture** — The host signs the exact offer and answer, including the DTLS fingerprints. Controllers verify the signature and host identity before configuring WebRTC. Password peers exchange a transcript-bound proof after the four data channels open. The host serializes verification and lockout across both transports before admitting the peer.

**Tech stack** — Go standard cryptography packages and the existing Pion, Dart cryptography, crypto, flutter_webrtc, and shared_preferences packages.

**Spec** — [Connection authentication v2](../specs/2026-11-26-connection-auth-v2-design.md)

## Global constraints

- Work in `.kkdesk-auth2` on `feat/auth-v2`. Commit this plan first and each completed task separately, with an English message and the requested Claude co-author trailer.
- Require v2 for password connections. Keep the paired phone's signed exchange, admission checks, and biometric behavior.
- Preserve the LAN server-first greeting, mobile demultiplexing, four concurrent handshakes, and 256 KiB signal limit. The greeting is `{"version":2}`. Update its Go and Dart readers. The upgrade prompt is displayed by new clients. Release notes tell users to upgrade both participants.
- A valid persistent host identity is required for admission. Use only existing cryptographic libraries.
- Before each task commit and again after all tasks, run the matrix below from the worktree. Run the Flutter commands for any task that changes mobile files. Record exit status and key results.
- Sign and hash the exact UTF-8 SDP sent over signaling. Use unpadded base64url for signatures and public keys. Both platforms read `internal/session/testdata/auth-v2.json`.

```sh
gofmt -l .
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./internal/session ./internal/app ./internal/pairing
GOOS=linux CGO_ENABLED=0 go vet ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ go vet ./...
(cd mobile && /Users/admin/development/flutter/bin/flutter pub get --offline &&
 /Users/admin/development/flutter/bin/dart analyze lib test &&
 /Users/admin/development/flutter/bin/flutter test)
```

## Review focus

- Sign and verify the same SDP bytes across Pion, rendezvous, and Flutter, including trailing CRLF and ICE candidates.
- Check a stored LAN key before sending a proof. Record the first key only after password authentication and enforce compare-and-set on concurrent first connections.
- Discard auth frames with an invalid format and keep waiting. Reject legacy peers and ignore ordinary meta traffic while awaiting a proof.
- Count at most one attempt per peer and serialize the lockout decision across LAN and cross-network transports.
- Validate a fresh invite and the phone signature before restoring a pairing. Bind the recovered result to the current signed pairing transcript.

## Task 1 — Authentication primitives and fixed vectors

**Files** — `internal/pairing/store.go`, `internal/session/auth.go`, `internal/session/auth_test.go`, `internal/session/testdata/auth-v2.json`.

- [ ] Write `TestAuthV2Vector`, `TestSignedRemoteAnswer`, and `TestPasswordLockout`. Assert fixture bytes, signature rejection for a changed offer, answer, or key, device-code mismatch, line-start rules for attributes, proof mismatch for a changed transcript or password, and the 5/30s/60s/15m/reset schedule. Check that paused attempts do not extend lockout.
- [ ] Run `go test ./internal/session -run 'Test(AuthV2Vector|SignedRemoteAnswer|PasswordLockout)$'` and observe the missing implementation.
- [ ] Add `pairing.DeviceCode(public []byte) string` and `(*pairing.Store).SignHostAnswer(offer, answer string) string`, with verification beside the session proof helpers. Define `PasswordGate.Check(now time.Time, transcript PasswordTranscript, proof []byte, temporary, fixed string) (string, time.Duration)` and `PasswordGate.Remaining(now time.Time) time.Duration`. Serialize checking both candidates and updating the shared state. The attempt that starts a lockout returns wrong-password. Subsequent attempts get the remaining duration.
- [ ] Run the targeted tests and full Go matrix, then commit.

## Task 2 — Go signaling, password admission, and status

**Files** — `internal/session/{signal,remote,peer,mobile,quality}.go`, session integration tests, `internal/app/{host,remote,viewer,pairing}.go`, app tests, `main.go`, `internal/ui/connect_page.go`.

- [ ] Write loopback tests for signed LAN and rendezvous answers, both passwords, invalid or missing proofs, a wrong host identity, lockout shared across transports, and early meta/input/file/clipboard traffic. Update existing tests that exercised the retired no-password path to test the configured confirmation flow with a password.
- [ ] Run the targeted tests and observe failures.
- [ ] Add the signed v2 LAN greeting and answer, and append the signed answer attributes for cross-network signaling. Verify the answer and expected identity before setting the remote description. Retire the cleartext password messages and old offer attribute.
- [ ] Use meta frame `0x41` plus 32 proof bytes and `0x52` plus result code (0 success, 1 wrong, 2 locked followed by a big-endian uint32 rounded-up seconds). Require password proof within 10 seconds of peer readiness and gate ordinary meta traffic in both directions during authentication. Permit goodbye, and preserve the latest quality request for admission. Paired phone peers continue to use the existing signed offer.
- [ ] Pass the same password gate to LAN and cross-network verification. Record the matched password before admission, retain the existing password rotation guard, expose lockout status and local device code in the host card, and fail closed with an identity path error if the key cannot be loaded.
- [ ] Run the targeted tests and full Go matrix, then commit.

## Task 3 — Desktop LAN identity persistence

**Files** — `internal/config/{devices,config_test}.go`, `internal/app/{viewer,app_test}.go`, `internal/ui/main_window.go`, `main.go`.

- [ ] Write tests for migration of existing entries, manual address lookup, first successful connection, reconnect, mismatched host rejection, removal, and concurrent first use.
- [ ] Run the targeted tests and observe failures.
- [ ] Store unpadded base64url in the optional JSON field `hostKey`. Bind the address via the existing normalization and a compare-and-set update in the store. Pass the expected key to the viewer and record a key after the password exchange succeeds. Show the first-use code for windowed and command-line connections. Reset the key when a device entry moves to a different address.
- [ ] Run the targeted tests and full Go matrix, then commit.

## Task 4 — Recover committed pairings

**Files** — `internal/app/{pairing,pairing_test}.go`, `mobile/lib/pairing/services.dart`, `mobile/test/pairing_integration_test.dart`.

- [ ] Write tests for requests signed by an already paired phone with valid and invalid invite proof, recovery across both invite types, and mobile persistence of a direct signed committed result. Exercise a lost final receipt.
- [ ] Run the targeted Go and Flutter tests and observe failures.
- [ ] Claim the same one-time invite used for new pairings before returning the saved record. For direct recovery, send the signed `committed` result with `exchange.HostNonce`, and verify it on the phone. Continue verifying the approval nonce on the normal path. Preserve existing phone settings and skip user confirmation on recovery.
- [ ] Run targeted tests and both verification matrices, then commit.

## Task 5 — Phone password sessions and LAN trust

**Files** — `mobile/lib/{lan_auth,lan_session,lan_protocol,lan_remote_session,main}.dart`, `mobile/lib/pairing/transport.dart`, phone remote and file-transfer UI, and relevant Flutter tests.

- [ ] Write tests for the shared signature and password fixture, mismatched or missing signatures, normalized address lookup, explicit forgetting, and auth-result decoding.
- [ ] Run the targeted Flutter tests and observe failures.
- [ ] Read the v2 greeting in both LAN transports, and send the exact gathered offer without credentials. Verify the signature and the shared persistent LAN identity before `setRemoteDescription`.
- [ ] Await the four data channels before sending the proof and block connection success, input, and file operations until the result. Store successful first-use identities in shared_preferences and show the device code. Add the confirmed forget action to the identity-error UI for both connection modes.
- [ ] Run targeted tests and both verification matrices, then commit.

## Task 6 — Guides and release notes

**Files** — `docs/guide/{security,connect-lan,remote}.md`, `README.md`, and release notes.

- [ ] Audit the current instructions for references to password challenges, confirmation in place of a password, and mixed-version connections.
- [ ] Update the guides and release notes for required upgrades, host signatures, LAN first use, identity changes, password lockout, and pairing recovery.
- [ ] Verify that the new guides and note cover every user-facing change and run the full Go matrix, then commit.

## Final verification

- [ ] Run both full verification matrices. Check the final diff and worktree status.
- [ ] Report each task's commit and checks, and any remaining Mac–Windows LAN, Mac–Windows cross-network, mobile LAN password and pairing-recovery device verification.
