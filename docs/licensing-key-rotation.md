# Rotating the entitlement signing key

The hub signs every entitlement token (RFC-006) with one Ed25519 key. The app,
the Go core inside it and the relay agent check tokens offline against the
public keys they trust. Each token names its key with a `key_id`: the first
8 bytes of the key's SHA-256, in hex. This runbook replaces that key without
signing anyone out, and revokes a key that leaked.

## What each side holds

| Where | Holds | Set by |
|---|---|---|
| Hub | the **active** private key (signs tokens and the key list) | `KERGUI_HUB_LICENSING_SIGNING_KEY` / `_FILE` |
| Hub | **next** public keys: published, signing nothing yet | `KERGUI_HUB_LICENSING_NEXT_PUBLIC_KEYS` |
| Hub | **retiring** public keys: no longer signing, published until their tokens are gone | `KERGUI_HUB_LICENSING_RETIRING_PUBLIC_KEYS` |
| Hub | **revoked** key ids: their tokens are refused everywhere | `KERGUI_HUB_LICENSING_REVOKED_KEY_IDS` |
| App build | the embedded public keys (one or more, comma separated) | `KERGUI_LICENSING_PUBLIC_KEY` (repository variable read by `mobile-release.yml`) |
| Relay agent | the same list, plus `revoked:<key id>` entries if needed | `KERGUI_LICENSING_PUBLIC_KEY` |

The lists take commas or spaces. Public keys are the raw 32 bytes in standard
base64, as `GET /licensing/v1/public-key` serves them. Private keys never go
in the repository.

`GET /licensing/v1/public-keys` serves every key that is not revoked, with its
status, the revoked ids, and `signed`: the same list signed by the active key.
At every refresh the app reads `signed`. It learns the list only when a key it
already trusts signed it, adds the new keys, and keeps every revocation for
good. It hands the result to the Go core (`SetPublicKeys`, with
`revoked:<id>` entries), then asks for a fresh token. A token of a revoked key
is dropped at once, and the same refresh replaces it. Nobody has to sign in
again.

## Timing

- A token lives at most **30 days** (`entitlement.TokenMaxLifetime`), plus
  **3 days** of offline grace (`entitlement.OfflineGrace`).
- **Overlap**, meaning how long the old key stays retiring after the
  promotion: **at least 33 days**. Make it longer if many phones go weeks
  without network.
- Shipping the next key in an app update first is not required: an app
  learns the next key from a list the current key signed. It protects the
  phones that never reach the hub between two steps, and the fresh installs
  of an old build after the old key is gone. Do it.

## Routine rotation

1. **Generate** the new key on a trusted machine:

   ```sh
   openssl genpkey -algorithm ed25519 -out licensing-next.key
   # public key, raw 32 bytes in base64:
   openssl pkey -in licensing-next.key -pubout -outform DER | tail -c 32 | base64
   ```

   Store `licensing-next.key` in the secret store next to the active key.
   The key id is the first 16 hex digits of
   `openssl pkey -in licensing-next.key -pubout -outform DER | tail -c 32 | sha256sum`.

2. **Add it as next** on the hub, then restart it:

   ```sh
   KERGUI_HUB_LICENSING_NEXT_PUBLIC_KEYS=<new public key>
   ```

   Check that `GET /licensing/v1/public-keys` lists it as `next`.

3. **Ship an app update that embeds it**. Set the repository variable
   `KERGUI_LICENSING_PUBLIC_KEY` to `<old public key>,<new public key>`, push a
   `mobile-v*` tag and publish the build. Wait until most installs run it
   (Play Console, release statistics). Also give the relay agents both keys.

4. **Promote it to active**. The hub now signs with the new key, and the old
   one retires:

   ```sh
   KERGUI_HUB_LICENSING_SIGNING_KEY_FILE=/run/secrets/licensing-next.key
   KERGUI_HUB_LICENSING_NEXT_PUBLIC_KEYS=
   KERGUI_HUB_LICENSING_RETIRING_PUBLIC_KEYS=<old public key>
   ```

   Restart the hub. Every refresh now hands a token of the new key, and the
   old tokens still verify. The start-up log says `key_id=<new id>`. The
   purchase page's form key comes from the signing key, so a page opened
   before the restart asks to be reloaded once.

5. **Retire the old key after the overlap** (33 days or more after step 4).
   Remove it:

   ```sh
   KERGUI_HUB_LICENSING_RETIRING_PUBLIC_KEYS=
   ```

   Its tokens have all expired by then. Destroy the old private key.

6. **Remove it from the app** at the next app release:
   `KERGUI_LICENSING_PUBLIC_KEY=<new public key>`.

## Emergency: the active key leaked

Anyone holding it can mint tokens and sign key lists until it is revoked, so
skip the overlap.

1. Generate a new key (step 1 above).
2. On the hub, sign with the new key and revoke the leaked one in the same
   restart:

   ```sh
   KERGUI_HUB_LICENSING_SIGNING_KEY_FILE=/run/secrets/licensing-new.key
   KERGUI_HUB_LICENSING_RETIRING_PUBLIC_KEYS=
   KERGUI_HUB_LICENSING_NEXT_PUBLIC_KEYS=
   KERGUI_HUB_LICENSING_REVOKED_KEY_IDS=<leaked key id>
   ```

3. Ship an app update that embeds only the new key, and set the relay agents
   to `<new public key>,revoked:<leaked key id>`.

An app that never learned the new key cannot verify the list the new key
signs, so it does not learn the revocation from the hub. It keeps working on
its cached token until that token expires, and the update brings it the new
key. To learn the revocation from the hub before the update, a phone must have
seen the new key as `next` before the switch. That is one more reason to
publish a next key early, even with no rotation planned.

## Checks

- `GET /licensing/v1/public-keys`: one `active` key, the expected `next` and
  `retiring` ones, and the revoked ids.
- The hub refuses to start on a contradictory configuration: the active key
  also listed as next or retiring, a key listed twice, or a revoked id still
  published.
- Tests: `internal/entitlement` (`keys_test.go`), `mobilecore`
  (`TestGateKeyRotation`), the app (`keyring.test.ts`, the
  `signing key rotation` tests in `entitlement.test.ts`), and the hub end to
  end on a fake clock (`internal/hub/e2e/key_rotation_e2e_test.go`, run by
  `make hub-test`).
