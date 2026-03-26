# Vault Encryption Testing Steps

## Prerequisites

```bash
go build -o squadron ./cmd/cli
```

## 1. Unit Tests

```bash
go test ./config/vault/... -v
```

Covers: round-trip encrypt/decrypt, empty map, wrong passphrase, corrupt file, randomness, passphrase generation.

## 2. Init Required

```bash
# Remove any existing vault
rm -f ~/.squadron/vars.vault

# Vars commands should fail without init
squadron vars set foo bar
# Expected: error "Squadron not initialized. Run 'squadron init' first."

squadron vars list
# Expected: same error
```

## 3. Squadron Init

```bash
squadron init
# Expected: creates ~/.squadron/vars.vault, stores passphrase in OS keychain

# Verify vault file exists
ls -la ~/.squadron/vars.vault

# Idempotent — running again is a no-op
squadron init
# Expected: "Squadron is already initialized."
```

## 4. Vars Set/Get/List

```bash
squadron vars set test_key hello_world
squadron vars get test_key
# Expected: hello_world

squadron vars set secret_key sk-ant-12345
squadron vars list
# Expected: both variables listed

# Verify vars.txt does NOT exist (everything is in the vault)
ls ~/.squadron/vars.txt
# Expected: file not found
```

## 5. Migration from vars.txt

```bash
# Remove vault to simulate legacy state
rm ~/.squadron/vars.vault

# Create a legacy vars.txt
echo -e "legacy_key=legacy_value\nother_key=other_value" > ~/.squadron/vars.txt

# Init should migrate
squadron init
# Expected: "Migrating 2 variables from vars.txt to encrypted vault..."

# Verify migration
squadron vars get legacy_key
# Expected: legacy_value

# vars.txt should be deleted
ls ~/.squadron/vars.txt
# Expected: file not found
```

## 6. Serve --init (Docker simulation)

```bash
# Remove vault
rm -f ~/.squadron/vars.vault

# Serve without --init should fail
squadron serve -c ./some-config
# Expected: error "Squadron not initialized. Run 'squadron init' or pass --init."

# Serve with --init should auto-initialize
squadron serve --init -c ./some-config -w --no-browser &
# Expected: initializes vault, starts serving
kill %1
```

## 7. Passphrase File

```bash
rm -f ~/.squadron/vars.vault

# Init with custom passphrase
echo "my-custom-passphrase" > /tmp/pp.txt
squadron init --passphrase-file /tmp/pp.txt
rm /tmp/pp.txt

# Set a variable
squadron vars set pf_test works

# Verify it works with the passphrase file
squadron vars get pf_test --passphrase-file <(echo "my-custom-passphrase")
# Expected: works
```

## 8. Wrong Passphrase

```bash
# Try to read with wrong passphrase
echo "wrong-passphrase" > /tmp/wrong.txt
squadron vars get pf_test --passphrase-file /tmp/wrong.txt
# Expected: error about wrong passphrase / decryption failure
rm /tmp/wrong.txt
```

## 9. Change Passphrase

```bash
echo "new-passphrase" > /tmp/new.txt
squadron vars change-passphrase --new-passphrase-file /tmp/new.txt
rm /tmp/new.txt

# Existing vars should still be accessible
squadron vars list
# Expected: all variables listed
```

## 10. Export

```bash
squadron vars export
# Expected: prints all key=value pairs in plaintext with a warning
```

## 11. Docker

```bash
docker compose build
docker compose up -d
# Expected: starts with --init, auto-initializes vault, logs warning about default passphrase

# Set a var inside the container
docker exec squadron-squadron-1 squadron vars set docker_test it_works
docker exec squadron-squadron-1 squadron vars get docker_test
# Expected: it_works

docker compose down
```

## 12. Docker with Secret

```bash
# Create a passphrase file
echo "docker-secure-passphrase" > vault_passphrase.txt

# Add to docker-compose.yml:
#   secrets:
#     - vault_passphrase
# ...
#   secrets:
#     vault_passphrase:
#       file: ./vault_passphrase.txt

docker compose up -d
# Expected: no fallback warning — uses the mounted secret

docker exec squadron-squadron-1 squadron vars set secure_test locked_down
docker exec squadron-squadron-1 squadron vars get secure_test
# Expected: locked_down

docker compose down
rm vault_passphrase.txt
```

## Summary Checklist

- [ ] Unit tests pass
- [ ] Vars commands fail without init
- [ ] `squadron init` creates vault and keychain entry
- [ ] `squadron init` is idempotent
- [ ] Set/get/list work with encrypted vault
- [ ] Legacy vars.txt migrated and deleted on init
- [ ] `serve --init` auto-initializes
- [ ] `--passphrase-file` works for init and vars commands
- [ ] Wrong passphrase is rejected
- [ ] Change passphrase works
- [ ] Export prints plaintext
- [ ] Docker works with `--init` (fallback passphrase)
- [ ] Docker works with mounted secret (full security)
