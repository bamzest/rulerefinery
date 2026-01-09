#!/usr/bin/env bash

# Bail out early if not running under bash (e.g., invoked with sh)
if [[ -z "${BASH_VERSION:-}" ]]; then
    echo "This script requires bash. Please run with: bash $0" >&2
    exit 1
fi

set -euo pipefail

API_URL="${GITHUB_META_URL:-https://api.github.com/meta}"
OUTPUT_FILE="rule_config/custom/github.list"

TMP_JSON="$(mktemp)"
TMP_IP_LIST="$(mktemp)"
TMP_DOMAIN_LIST="$(mktemp)"

cleanup() {
    rm -f "${TMP_JSON}" "${TMP_IP_LIST}" "${TMP_DOMAIN_LIST}"
}
trap cleanup EXIT

headers=(
    -H "Accept: application/vnd.github+json"
    -H "User-Agent: rulerefinery-meta-sync"
    -H "X-GitHub-Api-Version: 2022-11-28"
)

if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    headers+=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
fi

echo "Fetching GitHub meta from ${API_URL}..."
status_code="$(curl -w '%{http_code}' -o "${TMP_JSON}" -sS "${headers[@]}" "${API_URL}")" || {
    echo "Failed to fetch GitHub meta from ${API_URL}" >&2
    exit 1
}

if [[ "${status_code}" -ge 400 ]]; then
    echo "GitHub meta request failed with status ${status_code}. Set a valid GITHUB_TOKEN to avoid rate limits or check the token scope." >&2
    cat "${TMP_JSON}" >&2
    exit 1
fi

jq -r '
  [
    paths(scalars) as $p
    | getpath($p)
    | select(type == "string")
    | select(test("^(\\d{1,3}\\.){3}\\d{1,3}(/\\d{1,2})?$|^[0-9A-Fa-f:]+/[0-9]{1,3}$"))
  ]
  | unique
  | .[]
' "${TMP_JSON}" |
    while IFS= read -r cidr; do
        if [[ "${cidr}" == *:* ]]; then
            echo "IP-CIDR6,${cidr}"
        else
            echo "IP-CIDR,${cidr}"
        fi
    done |
    sort -u >"${TMP_IP_LIST}"

# Extract domain-like strings and convert to Clash rules
jq -r '
  [
    paths(scalars) as $p
    | getpath($p)
    | select(type == "string")
    | select(test("^(\\*\\.)?([A-Za-z0-9-]+\\.)+[A-Za-z]{2,}$"))
  ]
  | unique
  | .[]
' "${TMP_JSON}" |
    while IFS= read -r host; do
        if [[ "${host}" == "*."* ]]; then
            echo "DOMAIN-SUFFIX,${host#*.}"
        else
            echo "DOMAIN,${host}"
        fi
    done |
    sort -u >"${TMP_DOMAIN_LIST}"

{
    echo "# GitHub Meta IP ranges (auto-generated from ${API_URL})"
    echo "# Generated at $(date -u +'%Y-%m-%dT%H:%M:%SZ')"
    if [[ -s "${TMP_DOMAIN_LIST}" ]]; then
        echo "# Domains"
        cat "${TMP_DOMAIN_LIST}"
    fi
    echo "# IP ranges"
    cat "${TMP_IP_LIST}"
} >"${OUTPUT_FILE}"

echo "Updated ${OUTPUT_FILE} with $(wc -l <"${TMP_DOMAIN_LIST}") domains and $(wc -l <"${TMP_IP_LIST}") IP ranges."
