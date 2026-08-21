# UPDATE.md — Panduan Update grok2api (dengan local patches)

> Repo ni ada **12 local commits** di atas upstream `chenyme/grok2api`.
> Fail ni sebagai rujukan bila nak update ke versi baru.
> **Amalan: bagitahu agent AI check dulu sebelum merge.**

## Kedudukan semasa

| Item | Nilai |
|---|---|
| Branch aktif | `main` |
| Bookmark patches | `local-patches` (commit 47596974) |
| Jumlah local commits | 12 (909bb810, 6a954e5b, c541a436, b83b3e8b, 5b880b92, 6fd81ada, 47596974, 17080ba8, 04da9cc0, 15fb4147, 54c29d41, 93a74557) |
| Base upstream terakhir | `c9916f65` (19 Ogos 2026) |
| Image Docker | `grok2api:local-nltools` |
| Container | `grok2api` (docker compose) |

## Toolchain (WAJIB sebelum merge)

Go **1.26.7** dipasang via winget (`GoLang.Go`) — padan dengan `go 1.26` dalam `backend/go.mod`.
Tak masuk PATH global, jadi setiap sesi:

```powershell
$env:PATH = "C:\Program Files\Go\bin;$env:PATH"
cd backend; go build ./...; go test ./...
```

Baseline sihat: **62 pakej lulus, 0 gagal**.

**Penting:** CI repo ni (`.github/workflows/`) hanya ada CodeQL + docker image — **tiada `go test` automatik**.
Jadi `go test ./...` manual adalah satu-satunya jaring keselamatan selepas merge. Jangan skip.

`gofmt -l` akan flag ~12 fail dalam repo — itu **CRLF sahaja** (checkout Windows), bukan isu format.
Confirm dengan `gofmt -d <fail>` kalau ragu.

## Local patches yang wajib kekal (semak selepas setiap update)

| # | Patch | Fail | Kesan kalau hilang |
|---|---|---|---|
| 1 | Reasoning summary `detailed` untuk high/xhigh | `cli/normalize.go` | xhigh rasa "lembik", jawapan cetek |
| 2 | Doom-loop: counter berasingan (content **128** / reasoning 256) + ujian | `conversation/stream.go`, `stream_doomloop_test.go` | Stream xhigh dipotong awal; jadual/garis markdown kena bunuh |
| 3 | Soft-session identity v4 | `gateway/prompt_cache.go` | Gejala "ulang ayat sama" antara chat |
| 4 | max_output_tokens 65536 + alias inherit | `inference/handler.go` | Output terhad 16k, reasoning makan budget |
| 5 | Default max_tokens 65536 injection | `cli/adapter.go` | Request tanpa max_tokens dapat upstream default kecil |
| 6 | Persona gateway (AKIF, config.yaml `persona:`) | `cli/adapter.go`, `config/config.go`, `app/application.go` | IDE lain (Cursor dll) tak dapat persona |
| 7 | Persona hot-reload | `app/application.go` | Tukar persona kena restart container |
| 8 | reasoning_opaque replay multi-turn | `conversation/chat_request.go` | Chain-of-thought hilang antara turns |
| 9 | Buang persona generik, **tambah** `system_fingerprint` | `conversation/chat_request.go`, `chat_response.go`, `chat_stream.go` | Persona kosmetik override suara model; client OpenAI hilang `system_fingerprint` |

**Nota:** Persona AKIF settings hidup dalam `config.yaml` (gitignored — **tak ikut git**). Backup ada kat `../backups/config.yaml.persona_*.bak`.

### Liputan ujian setiap patch (audit 22 Ogos)

Semua patch kini ada ujian. Jalankan `go test ./...` selepas merge — **62 pakej, 0 gagal** = sihat.

| Patch | Fail ujian |
|---|---|
| 1. Reasoning `detailed` | `cli/normalize_test.go` (dah ada sebelum ni) |
| 2. Doom-loop | `conversation/stream_doomloop_test.go` |
| 3. Soft-session v4 | `gateway/soft_session_test.go` |
| 4+5. max_output 65536 | `inference/max_output_tokens_test.go`, `cli/max_output_tokens_test.go` |
| 6+7. Persona | `cli/persona_inject_test.go`, `config/persona_test.go` |
| 8. reasoning_opaque replay | `conversation/reasoning_replay_test.go` |

### ⚠️ KNOWN GAP: persona dilangkau bila `"system": []`

`isEmptyJSON` (`cli/normalize.go`) anggap hanya ``, `null`, `""` sebagai kosong — **bukan** `[]`.
Jadi bila client Anthropic hantar `"system": []` (array blok kosong), ia dikira "client dah bagi system prompt",
persona dilangkau, dan request sampai ke upstream **tanpa arahan sama sekali**.

SDK Anthropic yang sentiasa hantar key `system` dan biar caller append blok akan terkena.

Dipin sebagai ujian (`TestInjectPersonaIntoMessagesRequestSkipsPersonaForEmptyBlockArray`), **belum dibaiki** —
sebab `isEmptyJSON` dikongsi dengan laluan normalisasi lain, jadi kena periksa blast radius dulu.

### Nota: alias effort hanya diwarisi kalau model sokong level tu

`grok-4.5-xhigh` **bukan** alias sah — grok-4.5 berhenti di `high`, hanya grok-4.6 ada `xhigh`.
Jadi ia jatuh ke heuristik 10% context, bukan warisi 64k. Ini betul, bukan bug.
Dipin dalam `TestModelMaxOutputTokensUnsupportedEffortSuffixIsNotAnAlias`.

### Patch mana upstream dah ada, mana masih eksklusif kita

Disemak pada 22 Ogos 2026 terhadap `origin/main` (`d6f6e9f5`) — **jangan assume ikut tajuk commit sahaja, grep kod**:

| Patch | Upstream ada? | Nota |
|---|---|---|
| Doom-loop (semua) | ❌ **takda sama sekali** | `git grep -n "DoomLoop\|RepeatCount" origin/main` = kosong. `doom_loop_check` yang muncul tu event Grok CLI, benda lain. |
| reasoning_opaque | ❌ takda | `git grep -n "reasoning_opaque" origin/main` = kosong |
| Reasoning `detailed`, persona, max_tokens 65536 | ❌ takda | `normalize.go`, `adapter.go`, `prompt_cache.go` tak disentuh upstream — auto-merge bersih |

**PR dihantar ke upstream:** [chenyme/grok2api#994](https://github.com/chenyme/grok2api/pull/994) — doom-loop split thresholds + ujian.
Fork: `sofwanwork/grok2api`, remote `fork`, branch `feat/split-doom-loop-thresholds`.
Kalau PR ni diterima, **buang patch #2 dari senarai atas** dan konflik `stream.go` hilang selamanya.

### Yang upstream ada tapi kita belum (dapat bila merge)

| Feature | Commit | Kenapa berguna |
|---|---|---|
| Kesan 降智 + tukar akaun automatik | `d1aeb775`, `6b288f20` | Grok bagi jawapan tanpa berfikir → rotate akaun |
| Audit request + retention 7 hari | `1316ed73`, `86010fc5` | Diagnostik bila request pelik |
| Proxy per-akaun (`account-bound leases`) | `c0d7c94e` | Penting untuk multi-akaun |
| Video protokol `mediaGenInput` terkini | `46cfa374` | Video gen kita dah obsolete |

Default upstream pun berubah: `holdTimeout 3s→30s`, `minOutputTokens 32→8`, `accountCooldown 24h→12h`,
tambah `idleAccountCooldown: 15m`, dan `requestRetry.enabled: false→true`.

### ⚠️ config.yaml kita tertinggal 3 section

`config.yaml` kita **takda** `deployment:`, `audit:`, `qualityGuard:` — semua tuning baru di atas tak aktif.
Sebab `config.yaml` gitignored, kena tambah **manual** dari `config.example.yaml` selepas merge.

### Kos konflik merge (dry-run 22 Ogos)

`git merge-tree --write-tree HEAD origin/main` → **11 fail konflik**:

- **Bermakna (4):** `conversation/stream.go`, `inference/handler.go`, `handler_test.go`, `conversation_test.go` — kena **gabung dua-dua**, bukan pilih satu
- **Kosmetik (7):** `egress/service.go`, `web/image.go`, `web/video.go`, `openai_audio_handler.go`, `voice_handler.go`, `config.example.yaml`, `i18n/index.ts` — semua dari commit terjemahan BM/EN (`17080ba8`, `04da9cc0`, `15fb4147`, `54c29d41`)

**64% kerja merge adalah kosmetik.** Kalau patch terjemahan di-drop, konflik turun dari 11 → 4.
Pertimbangkan bila terjemahan tu dah tak berbaloi.

## Senarai semak bila ada update upstream

### Sebelum merge (WAJIB)
1. **Backup config:**
   ```powershell
   copy config.yaml ..\backups\config.yaml.preupdate.bak
   ```
2. **Pastikan working tree bersih:**
   ```powershell
   git status   # mesti "nothing to commit"
   ```
3. **Fetch:**
   ```powershell
   git fetch origin
   ```

### Merge
```powershell
git merge origin/main
```

**Kalau konflik** (fail dengan `<<<<<<<`):
- Fail berisiko tinggi: `normalize.go`, `stream.go`, `chat_request.go`, `adapter.go`, `handler.go`
- Selesaikan dengan **kekalkan bahagian patch kita** melainkan upstream ada fix yang lebih baik (bandingkan dulu)
- Atau **panggil agent AI**: "check git conflict ni, patch mana yang patut kekal"

### Selepas merge (WAJIB)
1. **Build + test:**
   ```powershell
   docker run --rm -v "${PWD}\backend:/src" -w /src golang:1.26 go test ./...
   ```
2. **Rebuild image:**
   ```powershell
   docker build -t grok2api:local-nltools .
   docker compose up -d
   ```
3. **Verify patch masih ada** — quick test:
   ```powershell
   # Persona masih inject? (mesti keluar AKIF style)
   curl -s -X POST http://127.0.0.1:8000/v1/chat/completions -H "Authorization: Bearer <KEY>" -H "Content-Type: application/json" -d '{"model":"grok-4.6-xhigh","messages":[{"role":"user","content":"hi"}],"stream":false}'
   ```
   - ✅ Persona: jawapan mula dengan emosi AKIF ("Wah...", "Aduh...")
   - ❌ Persona hilang: jawapan neutral → check `config.yaml` section `persona:` masih `enabled: true`

4. **Verify limits:**
   ```powershell
   curl -s http://127.0.0.1:8000/v1/models -H "Authorization: Bearer <KEY>"
   # grok-4.6-xhigh mesti: context=500000, output=65536
   ```

### Kalau benda rosak teruk (fallback)
```powershell
# Balik ke patch kita, buang merge
git merge --abort          # kalau masih dalam merge
git reset --hard local-patches   # kalau dah commit tapi rosak

# Atau extract diff kita untuk apply semula atas upstream fresh
git diff local-patches main -- backend/ > my-patches.diff
```

## Benda lain yang kat luar repo grok2api (kena jaga sendiri)

| Fail | Lokasi | Fungsi |
|---|---|---|
| `config.yaml` | `Desktop\grokapi\grok2api\` | Persona AKIF + semua secrets (gitignored!) |
| `AGENTS.md` | `~\.config\opencode\` + `Desktop\grokapi\` | Persona AKIF untuk OpenCode |
| `opencode.json` | `~\.config\opencode\` | Limit 500k/64k + compaction 80k/30turns |

Backup semua ni dalam `Desktop\grokapi\backups\`.

## Command cheat sheet

```powershell
# Check ada update baru?
git fetch origin
git log HEAD..origin/main --oneline    # senarai commit baru yang belum ada

# Jumpa perubahan pada fail yang kita patch?
git diff HEAD...origin/main --stat -- backend/internal/infra/provider/cli/normalize.go

# Image lama sebagai fallback
docker tag grok2api:local-nltools grok2api:backup-$(Get-Date -Format "yyyyMMdd")
```
