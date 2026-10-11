#!/usr/bin/env bash
# Installs ScoutJobs: builds the CLI and links the skills into every agent that
# follows the Agent Skills standard. Safe to re-run. Nothing is copied into the
# repo; your data stays in ~/.scoutjobs.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="${SCOUTJOBS_BIN_DIR:-$HOME/.local/bin}"
PLATFORMS="${SCOUTJOBS_PLATFORMS:-claude codex agents cursor gemini copilot}"

say() { printf '%s\n' "$*"; }

link_skills() { # $1 = skills dir of the platform
  mkdir -p "$1"
  for d in "$ROOT"/skills/*/; do
    name="$(basename "$d")"
    target="$1/$name"
    if [ -L "$target" ] || [ ! -e "$target" ]; then
      ln -sfn "$d" "$target"
    else
      say "  skip $target (exists, not a symlink)"
    fi
  done
  say "  linked skills into $1"
}

# Codex has no plugin commands, but it reads custom prompts from ~/.codex/prompts/<name>.md
# and offers them as /name. One prompt per commands/*.md, frontmatter stripped, so
# /scoutjobs-send in Codex does what /scoutjobs:send does in Claude Code.
link_prompts() { # $1 = prompts dir
  mkdir -p "$1"
  for f in "$ROOT"/commands/*.md; do
    name="scoutjobs-$(basename "$f" .md)"
    {
      awk 'BEGIN{fm=0} /^---$/{fm++; next} fm>=2{print}' "$f"
      printf '\nNo subagents here: where the text says to spawn an agent, use the skill of the same job (draft-review, fit-score, behavioral-interview, coding-interview, sysdesign-interview, behavioral-stories) and do it yourself in this conversation.\n' "$ROOT"
    } > "$1/$name.md"
  done
  say "  prompts written to $1 (/scoutjobs-brief, /scoutjobs-send, ...)"
}

say "ScoutJobs install"

if command -v go >/dev/null 2>&1; then
  say "building scoutjobs CLI into $BIN_DIR"
  mkdir -p "$BIN_DIR"
  (cd "$ROOT" && go build -ldflags "-s -w -X github.com/zoolcoder/scoutjobs/internal/cli.Version=$(git -C "$ROOT" describe --tags --always 2>/dev/null || echo dev)" -o "$BIN_DIR/scoutjobs" ./cmd/scoutjobs)
  case ":$PATH:" in *":$BIN_DIR:"*) ;; *) say "  add $BIN_DIR to PATH";; esac
else
  say "go not found: downloading the latest release binary into $BIN_DIR"
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$(uname -m)" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) arch="";; esac
  if [ -z "$arch" ] || { [ "$os" != darwin ] && [ "$os" != linux ]; }; then
    say "  no prebuilt binary for $os/$(uname -m): install Go 1.24+ and re-run, or see https://github.com/zoolcoder/scoutjobs/releases"
  else
    mkdir -p "$BIN_DIR"
    url="https://github.com/zoolcoder/scoutjobs/releases/latest/download/scoutjobs_${os}_${arch}.tar.gz"
    if curl -fsSL "$url" | tar -xz -C "$BIN_DIR" scoutjobs; then
      chmod +x "$BIN_DIR/scoutjobs"
      say "  installed $("$BIN_DIR/scoutjobs" version 2>/dev/null || echo scoutjobs) from $url"
      case ":$PATH:" in *":$BIN_DIR:"*) ;; *) say "  add $BIN_DIR to PATH";; esac
    else
      say "  download failed ($url): install Go 1.24+ and re-run, or brew install --cask zoolcoder/scoutjobs/scoutjobs"
    fi
  fi
fi

for p in $PLATFORMS; do
  case "$p" in
    claude)  say "claude code"; link_skills "$HOME/.claude/skills"
             mkdir -p "$HOME/.claude/agents"; for a in "$ROOT"/agents/*.md; do ln -sfn "$a" "$HOME/.claude/agents/$(basename "$a")"; done
             say "  tip: for slash commands install as a plugin instead: claude plugin marketplace add zoolcoder/scoutjobs && claude plugin install scoutjobs@scoutjobs";;
    codex)   say "codex"; link_skills "$HOME/.codex/skills"; mkdir -p "$HOME/.codex"; link_prompts "$HOME/.codex/prompts"
             say "  append adapters/codex/AGENTS.md to ~/.codex/AGENTS.md if you want the usage rules";;
    agents)  say "agent skills (shared)"; link_skills "$HOME/.agents/skills";;
    cursor)  say "cursor"; link_skills "$HOME/.cursor/skills"; mkdir -p "$HOME/.cursor/rules"; ln -sfn "$ROOT/adapters/cursor/scoutjobs.mdc" "$HOME/.cursor/rules/scoutjobs.mdc"; say "  rule linked into ~/.cursor/rules";;
    gemini)  say "gemini cli"; link_skills "$HOME/.gemini/skills"; say "  append adapters/gemini/GEMINI.md to ~/.gemini/GEMINI.md if you want the usage rules";;
    copilot) say "copilot"; link_skills "$HOME/.copilot/skills"; say "  copy adapters/copilot/copilot-instructions.md into your workspace's .github/";;
    *) say "unknown platform $p";;
  esac
done

# persona files are referenced by the mock-interview skill on platforms without subagents
mkdir -p "$HOME/.scoutjobs"
ln -sfn "$ROOT/agents" "$HOME/.scoutjobs/agents"

say
say "next: scoutjobs init && scoutjobs serve   (dashboard at http://127.0.0.1:7070)"
say "then in your agent: run the scoutjobs-setup skill"
