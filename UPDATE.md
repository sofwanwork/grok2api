# UPDATE.md — Panduan Update grok2api (dengan local patches)

> Repo ni ada **6 local commits** di atas upstream `chenyme/grok2api`.
> Fail ni sebagai rujukan bila nak update ke versi baru.
> **Amalan: bagitahu agent AI check dulu sebelum merge.**

## Kedudukan semasa

| Item | Nilai |
|---|---|
| Branch aktif | `main` |
| Bookmark patches | `local-patches` (commit 47596974) |
| Jumlah local commits | 6 (909bb810, 6a954e5b, c541a436, b83b3e8b, 5b880b92, 6fd81ada, 47596974) |
| Image Docker | `grok2api:local-nltools` |
| Container | `grok2api` (docker compose) |

## Local patches yang wajib kekal (semak selepas setiap update)

| # | Patch | Fail | Kesan kalau hilang |
|---|---|---|---|
| 1 | Reasoning summary `detailed` untuk high/xhigh | `cli/normalize.go` | xhigh rasa "lembik", jawapan cetek |
| 2 | Doom-loop: counter berasingan (content 32 / reasoning 256) | `conversation/stream.go` | Stream xhigh dipotong awal |
| 3 | Soft-session identity v4 | `gateway/prompt_cache.go` | Gejala "ulang ayat sama" antara chat |
| 4 | max_output_tokens 65536 + alias inherit | `inference/handler.go` | Output terhad 16k, reasoning makan budget |
| 5 | Default max_tokens 65536 injection | `cli/adapter.go` | Request tanpa max_tokens dapat upstream default kecil |
| 6 | Persona gateway (AKIF, config.yaml `persona:`) | `cli/adapter.go`, `config/config.go`, `app/application.go` | IDE lain (Cursor dll) tak dapat persona |
| 7 | Persona hot-reload | `app/application.go` | Tukar persona kena restart container |
| 8 | reasoning_opaque replay multi-turn | `conversation/chat_request.go` | Chain-of-thought hilang antara turns |
| 9 | Buang persona generik + system_fingerprint | `conversation/chat_request.go`, `chat_response.go`, `chat_stream.go` | Persona kosmetik override suara model |

**Nota:** Persona AKIF settings hidup dalam `config.yaml` (gitignored — **tak ikut git**). Backup ada kat `../backups/config.yaml.persona_*.bak`.

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
