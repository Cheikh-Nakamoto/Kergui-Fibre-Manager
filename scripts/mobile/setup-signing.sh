#!/usr/bin/env bash
# setup-signing.sh — create the mobile app's signing material and store it as
# GitHub Actions secrets with `gh`, for .github/workflows/mobile-release.yml.
#
# Nothing secret is ever printed or passed on a command line: passwords are
# generated with openssl, handed to keytool/openssl through environment
# variables, and to `gh secret set` on stdin. Every generated file is also
# copied to a backup directory OUTSIDE the repository (default
# ~/.kergui-signing) — keep it, then move it to a password manager or an
# encrypted drive.
#
# Usage: scripts/mobile/setup-signing.sh [options] <command> [args]
#
# Commands:
#   android                        Generate the Android upload keystore (PKCS12, RSA 4096)
#   play-sa <service-account.json> Store the Google Play service account key (JSON)
#   ios-csr                        Generate the iOS distribution private key and CSR
#   ios-p12 <distribution.cer>     Build the .p12 from Apple's certificate and the key
#   asc-key <AuthKey.p8> <key-id> <issuer-id>
#                                  Store the App Store Connect API key
#   all                            android + ios-csr, then print the manual steps left
#   check                          List which expected secrets are set
#
# Options:
#   --repo OWNER/NAME   Target repository (default: the current gh repo)
#   --env NAME          GitHub environment holding the secrets (default: release)
#   --backup-dir DIR    Where generated files are kept (default: ~/.kergui-signing)
#   --dname DN          Android certificate subject (default: "CN=Kergui Fibre Manager, O=Kergui, C=SN")
#   --email ADDRESS     Email in the iOS CSR (default: git config user.email)
#   --force             Overwrite secrets that already exist
#   --dry-run           Do everything except writing to GitHub
#   -h, --help          Show this help

set -euo pipefail
umask 077

ENV_NAME=release
REPO=""
BACKUP_DIR="${HOME}/.kergui-signing"
DNAME="CN=Kergui Fibre Manager, O=Kergui, C=SN"
EMAIL=""
FORCE=0
DRY_RUN=0

ANDROID_SECRETS=(ANDROID_UPLOAD_KEYSTORE_B64 ANDROID_UPLOAD_STORE_PASSWORD ANDROID_UPLOAD_KEY_ALIAS ANDROID_UPLOAD_KEY_PASSWORD)
PLAY_SECRETS=(PLAY_SERVICE_ACCOUNT_JSON)
IOS_SECRETS=(IOS_DIST_P12_B64 IOS_DIST_P12_PASSWORD)
ASC_SECRETS=(ASC_KEY_P8_B64 ASC_KEY_ID ASC_ISSUER_ID)

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
info() { printf '>> %s\n' "$*" >&2; }
usage() { sed -n '2,/^$/{s/^# \{0,1\}//;p}' "$0"; }

need() {
	local tool
	for tool in "$@"; do
		command -v "$tool" >/dev/null 2>&1 || die "'$tool' is required but not installed"
	done
}

b64() { base64 <"$1" | tr -d '\n'; }

random_password() { openssl rand -base64 32 | tr -d '\n/+=' | cut -c1-32; }

# ---------------------------------------------------------------- setup

TMP=""
cleanup() {
	if [[ -n "$TMP" && -d "$TMP" ]]; then rm -rf "$TMP"; fi
}
trap cleanup EXIT

init_tmp() {
	TMP="$(mktemp -d)"
	chmod 700 "$TMP"
}

init_backup() {
	mkdir -p "$BACKUP_DIR"
	chmod 700 "$BACKUP_DIR"
	local backup top
	backup="$(cd "$BACKUP_DIR" && pwd -P)"
	if top="$(git rev-parse --show-toplevel 2>/dev/null)"; then
		top="$(cd "$top" && pwd -P)"
		case "$backup/" in
		"$top"/*) die "backup dir $backup is inside the repository $top: choose one outside (--backup-dir)" ;;
		esac
	fi
	BACKUP_DIR="$backup"
}

init_github() {
	need gh
	if [[ -z "$REPO" ]]; then
		REPO="$(gh repo view --json nameWithOwner -q .nameWithOwner)" || die "cannot find the repository; pass --repo OWNER/NAME"
	fi
	((DRY_RUN)) && { info "dry run: nothing is written to $REPO"; return; }
	gh auth status >/dev/null 2>&1 || die "gh is not authenticated: run 'gh auth login'"
	# Creating an environment that already exists is a no-op.
	gh api --silent -X PUT "repos/$REPO/environments/$ENV_NAME" >/dev/null ||
		die "cannot create the '$ENV_NAME' environment on $REPO (admin rights needed)"
}

EXISTING=""
load_existing() {
	((DRY_RUN)) && return 0
	EXISTING="$(gh secret list --repo "$REPO" --env "$ENV_NAME" --json name -q '.[].name')" || die "cannot list secrets of $REPO/$ENV_NAME"
}

secret_exists() { grep -qx "$1" <<<"$EXISTING"; }

# refuse_overwrite fails if any of the named secrets is already set (unless --force).
refuse_overwrite() {
	((FORCE)) && return 0
	load_existing
	local name clash=()
	for name in "$@"; do secret_exists "$name" && clash+=("$name"); done
	((${#clash[@]} == 0)) || die "already set in $REPO/$ENV_NAME: ${clash[*]} (use --force to replace them)"
}

# put_secret NAME < value — the value only ever travels on stdin.
put_secret() {
	local name="$1"
	if ((DRY_RUN)); then
		cat >/dev/null
		info "dry run: would set $name"
		return
	fi
	gh secret set "$name" --repo "$REPO" --env "$ENV_NAME" >/dev/null
	info "set $name"
}

backup_file() {
	local src="$1" name="$2"
	cp "$src" "$BACKUP_DIR/$name"
	chmod 600 "$BACKUP_DIR/$name"
}

# ---------------------------------------------------------------- commands

cmd_android() {
	need keytool openssl base64
	init_backup
	init_github
	refuse_overwrite "${ANDROID_SECRETS[@]}"
	init_tmp

	local alias=upload ks="$TMP/upload.jks"
	# PKCS12 keystores have a single password: keytool ignores a separate key
	# password, so both secrets carry the same value.
	KERGUI_STOREPASS="$(random_password)"
	export KERGUI_STOREPASS
	info "generating the Android upload keystore (RSA 4096, alias '$alias')"
	keytool -genkeypair -noprompt -storetype PKCS12 -keystore "$ks" -alias "$alias" \
		-keyalg RSA -keysize 4096 -validity 10000 -dname "$DNAME" \
		-storepass:env KERGUI_STOREPASS -keypass:env KERGUI_STOREPASS >/dev/null 2>&1 ||
		die "keytool failed"

	local stamp
	stamp="$(date +%Y%m%d-%H%M%S)"
	backup_file "$ks" "android-upload-$stamp.jks"
	printf 'ANDROID_UPLOAD_KEY_ALIAS=%s\nANDROID_UPLOAD_STORE_PASSWORD=%s\nANDROID_UPLOAD_KEY_PASSWORD=%s\n' \
		"$alias" "$KERGUI_STOREPASS" "$KERGUI_STOREPASS" >"$TMP/android.env"
	backup_file "$TMP/android.env" "android-upload-$stamp.env"

	b64 "$ks" | put_secret ANDROID_UPLOAD_KEYSTORE_B64
	printf '%s' "$KERGUI_STOREPASS" | put_secret ANDROID_UPLOAD_STORE_PASSWORD
	printf '%s' "$alias" | put_secret ANDROID_UPLOAD_KEY_ALIAS
	printf '%s' "$KERGUI_STOREPASS" | put_secret ANDROID_UPLOAD_KEY_PASSWORD

	info "upload certificate fingerprint (public; Play Console shows the same value):"
	keytool -J-Duser.language=en -list -v -keystore "$ks" -alias "$alias" -storepass:env KERGUI_STOREPASS 2>/dev/null |
		grep -E '^[[:space:]]*SHA256:' >&2 || true
	unset KERGUI_STOREPASS
	info "backup: $BACKUP_DIR/android-upload-$stamp.{jks,env}"
}

cmd_play_sa() {
	local json="${1:-}"
	[[ -f "$json" ]] || die "usage: play-sa <service-account.json>"
	grep -q '"type": *"service_account"' "$json" || die "$json is not a Google service account key"
	grep -q '"private_key"' "$json" || die "$json has no private_key"
	init_github
	refuse_overwrite "${PLAY_SECRETS[@]}"
	put_secret PLAY_SERVICE_ACCOUNT_JSON <"$json"
	info "you can now delete $json (Google Cloud keeps the key; create a new one if lost)"
}

cmd_ios_csr() {
	need openssl
	init_backup
	local key="$BACKUP_DIR/ios-dist.key" csr="$BACKUP_DIR/ios-dist.csr"
	if [[ -f "$key" && $FORCE -eq 0 ]]; then
		die "$key already exists (use --force to make a new one; the old certificate then stops matching)"
	fi
	if [[ -z "$EMAIL" ]]; then
		EMAIL="$(git config user.email 2>/dev/null || true)"
	fi
	[[ -n "$EMAIL" ]] || die "no email for the CSR: pass --email"
	info "generating the iOS distribution key and CSR"
	openssl genrsa -out "$key" 2048 2>/dev/null
	chmod 600 "$key"
	openssl req -new -key "$key" -out "$csr" -subj "/emailAddress=$EMAIL/CN=Kergui Distribution/C=SN"
	cat >&2 <<EOF
>> CSR written to $csr
   1. https://developer.apple.com/account/resources/certificates/add
   2. choose "Apple Distribution", upload $csr
   3. download distribution.cer, then run:
        $0 ios-p12 ~/Downloads/distribution.cer
EOF
}

cmd_ios_p12() {
	local cer="${1:-}"
	[[ -f "$cer" ]] || die "usage: ios-p12 <distribution.cer>"
	need openssl base64
	init_backup
	local key="$BACKUP_DIR/ios-dist.key"
	[[ -f "$key" ]] || die "$key not found: run 'ios-csr' first (on this machine, same --backup-dir)"
	init_github
	refuse_overwrite "${IOS_SECRETS[@]}"
	init_tmp

	local pem="$TMP/dist.pem" p12="$TMP/dist.p12"
	# Apple ships DER; accept PEM too.
	openssl x509 -inform DER -in "$cer" -out "$pem" 2>/dev/null ||
		openssl x509 -inform PEM -in "$cer" -out "$pem" 2>/dev/null ||
		die "$cer is not a certificate"
	[[ "$(openssl x509 -noout -pubkey -in "$pem" | openssl sha256)" == "$(openssl pkey -pubout -in "$key" | openssl sha256)" ]] ||
		die "$cer was not issued for $key (wrong CSR?)"

	KERGUI_P12PASS="$(random_password)"
	export KERGUI_P12PASS
	# 3DES/SHA1 encryption: the macOS keychain on the CI runner cannot import
	# the AES-256 PKCS12 that OpenSSL 3 writes by default.
	openssl pkcs12 -export -inkey "$key" -in "$pem" -out "$p12" -name "Kergui Distribution" \
		-keypbe PBE-SHA1-3DES -certpbe PBE-SHA1-3DES -macalg sha1 -passout env:KERGUI_P12PASS ||
		die "openssl pkcs12 failed"

	local stamp
	stamp="$(date +%Y%m%d-%H%M%S)"
	backup_file "$p12" "ios-dist-$stamp.p12"
	printf 'IOS_DIST_P12_PASSWORD=%s\n' "$KERGUI_P12PASS" >"$TMP/ios.env"
	backup_file "$TMP/ios.env" "ios-dist-$stamp.env"

	b64 "$p12" | put_secret IOS_DIST_P12_B64
	printf '%s' "$KERGUI_P12PASS" | put_secret IOS_DIST_P12_PASSWORD
	unset KERGUI_P12PASS
	info "certificate expires: $(openssl x509 -noout -enddate -in "$pem" | cut -d= -f2)"
	info "backup: $BACKUP_DIR/ios-dist-$stamp.{p12,env}"
}

cmd_asc_key() {
	local p8="${1:-}" key_id="${2:-}" issuer="${3:-}"
	[[ -f "$p8" && -n "$key_id" && -n "$issuer" ]] || die "usage: asc-key <AuthKey.p8> <key-id> <issuer-id>"
	grep -q -- '-----BEGIN PRIVATE KEY-----' "$p8" || die "$p8 is not a .p8 private key"
	[[ "$key_id" =~ ^[A-Z0-9]{10}$ ]] || die "key id should be 10 characters, e.g. ABC123DEFG"
	[[ "$issuer" =~ ^[0-9a-f-]{36}$ ]] || die "issuer id should be a UUID"
	need base64
	init_backup
	init_github
	refuse_overwrite "${ASC_SECRETS[@]}"
	backup_file "$p8" "AuthKey_$key_id.p8"
	b64 "$p8" | put_secret ASC_KEY_P8_B64
	printf '%s' "$key_id" | put_secret ASC_KEY_ID
	printf '%s' "$issuer" | put_secret ASC_ISSUER_ID
	info "backup: $BACKUP_DIR/AuthKey_$key_id.p8 (Apple lets you download a .p8 only once)"
}

cmd_check() {
	init_github
	((DRY_RUN)) && die "check reads GitHub: run it without --dry-run"
	load_existing
	local name missing=0
	for name in "${ANDROID_SECRETS[@]}" "${PLAY_SECRETS[@]}" "${IOS_SECRETS[@]}" "${ASC_SECRETS[@]}"; do
		if secret_exists "$name"; then
			printf '  ok       %s\n' "$name"
		else
			printf '  missing  %s\n' "$name"
			missing=$((missing + 1))
		fi
	done
	((missing == 0)) || { info "$missing secret(s) missing in $REPO/$ENV_NAME"; return 1; }
	info "all signing secrets are set in $REPO/$ENV_NAME"
}

cmd_all() {
	cmd_android
	cmd_ios_csr
	cat >&2 <<EOF

>> Still to do by hand:
   - Google Play Console: create the app, then a service account with the
     "Release manager" role, download its JSON key, and run:
       $0 play-sa <key.json>
   - Apple: upload the CSR (see above), then run: $0 ios-p12 <distribution.cer>
   - App Store Connect > Users and Access > Integrations: create an API key
     (App Manager role), then run: $0 asc-key <AuthKey_XXXX.p8> <key-id> <issuer-id>
   - Finally: $0 check
EOF
}

# ---------------------------------------------------------------- main

main() {
	local args=()
	while (($#)); do
		case "$1" in
		--repo) REPO="${2:?--repo needs a value}"; shift 2 ;;
		--env) ENV_NAME="${2:?--env needs a value}"; shift 2 ;;
		--backup-dir) BACKUP_DIR="${2:?--backup-dir needs a value}"; shift 2 ;;
		--dname) DNAME="${2:?--dname needs a value}"; shift 2 ;;
		--email) EMAIL="${2:?--email needs a value}"; shift 2 ;;
		--force) FORCE=1; shift ;;
		--dry-run) DRY_RUN=1; shift ;;
		-h | --help) usage; exit 0 ;;
		--) shift; args+=("$@"); break ;;
		-*) die "unknown option $1 (see --help)" ;;
		*) args+=("$1"); shift ;;
		esac
	done
	((${#args[@]})) || { usage; exit 2; }
	local cmd="${args[0]}"
	args=("${args[@]:1}")
	case "$cmd" in
	android) cmd_android ;;
	play-sa) cmd_play_sa "${args[@]}" ;;
	ios-csr) cmd_ios_csr ;;
	ios-p12) cmd_ios_p12 "${args[@]}" ;;
	asc-key) cmd_asc_key "${args[@]}" ;;
	all) cmd_all ;;
	check) cmd_check ;;
	*) die "unknown command '$cmd' (see --help)" ;;
	esac
}

main "$@"
