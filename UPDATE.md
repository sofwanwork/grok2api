# UPDATE.md — Panduan Update grok2api (dengan local patches)

> Repo ni ada **local patches di atas upstream `chenyme/grok2api`** (merge terakhir: 22 Ogos 2026).
> Fail ni sebagai rujukan bila nak update ke versi baru.
> **Amalan: bagitahu agent AI check dulu sebelum merge.**

## Kedudukan semasa

| Item | Nilai |
|---|---|
| Branch aktif | `main` |
| Bookmark patches | `local-patches` (kini sejajar dengan merge `bed7232d` — di-refresh 22 Ogos, jangan biar stale lagi) |
| Tag fallback | `backup-pre-merge-20260822` (keadaan pra-merge, 12 commit) |
| Base upstream terakhir | `d6f6e9f5` (19 Ogos 2026) — **merged 22 Ogos 2026** |
| Image Docker | `grok2api:local-nltools` (post-merge); fallback: `grok2api:backup-20260822`, `grok2api:local-layered` (pra-merge) |
| Container | `grok2api` (docker compose) |
| Verify tool | `powershell tools/verify-patches.ps1` atau `make verify VERIFY_ARGS=-SkipLive` |

## Merge 22 Ogos 2026 — apa yang berlaku

11 konflik diselesaikan, semua patch terselamat (verify: 6/6 marker PASS, live limits + persona PASS):

- **4 fail bermakna digabung dua-dua:** `stream.go` (patch reasoning_opaque + upstream markReasoningEvidence),
  `conversation_test.go` (test kita + test evidence upstream — assertion anti-leak upstream
  diadapt supaya `reasoning_opaque` yang disengajakan dianggap channel sah, bukan leak),
  `handler.go`/`handler_test.go` (BM kekal + fix upstream `bytes.NewReader(body)` untuk double-read bug +
  protokol `response.failed` baru)
- **Upstream buang protokol media/post lama:** `createMediaPost`, `parseMediaPostResponse`,
  `prepareVideoReference` dibuang (video dah guna `mediaGenInput` baharu) — kita ikut, kod itu bukan patch kita
- **3 section baru masuk config.yaml:** `deployment`, `audit` (retention 7 hari), `qualityGuard`
  (requestRetry enabled default, holdTimeout 30s, idleAccountCooldown 15m)
- **i18n:** locale zh-CN dibuang sekali lagi (kekal English-only), strings EN baru upstream auto-merge masuk

## Toolchain (WAJIB sebelum merge)

Go **1.26.7** dipasang via winget (`GoLang.Go`) — padan dengan `go 1.26` dalam `backend/go.mod`.
Tak masuk PATH global, jadi setiap sesi:

```powershell
$env:PATH = "C:\Program Files\Go\bin;$env:PATH"
cd backend; go build ./...; go test ./...
```

Baseline sihat: **62 pakej lulus, 0 gagal**.

**Penting:** UPDATE lama kata CI tiada `go test` — **tak betul lagi**. `ghcr-image.yml` job `verify`
dah jalankan `go test ./...` + `go vet` + swagger check + frontend lint/build pada setiap push/PR ke `main`.
Ia hanya berkesan bila repo di-push ke GitHub (fork `sofwanwork/grok2api`).

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
| 10 | Diagnostik hosted-tool tak dijalankan (trailer + audit) | `inference/hosted_tools.go`, `inference/handler.go`, `gateway/service.go`, `domain/audit/audit.go`, `relational/models.go` | Model claim "dah search web" tapi tak pernah search — halusinasi senyap tanpa amaran |
| 11 | Auto-retry tool-call degradasi + salvage XML | `gateway/tool_degradation.go` (baru), `gateway/tool_salvage.go` (baru), `gateway/quality_retry.go`, `gateway/quality_retry_scan.go`, `gateway/service.go`, `config/config.go` | ~50% request dengan argumen tool besar (400+ aksara) balik narasi prosa/XML bukan structured call — client terima teks sampah, tool tak pernah jalan |
| 12 | Placeholder reasoning bila CoT disorok | `conversation/chat_stream.go`, `conversation/messages_stream.go`, `conversation/stream.go` | Client nampak trace kosong sedangkan model fikir (reasoning_tokens > 0) — tiada beza dari model tak fikir langsung |

**Nota:** Persona AKIF settings hidup dalam `config.yaml` (gitignored — **tak ikut git**). Backup ada kat `../backups/config.yaml.persona_*.bak`.

### Liputan ujian setiap patch (audit 22 Ogos)

Semua patch kini ada ujian. Jalankan `go test ./...` selepas merge — **62 pakej, 0 gagal** = sihat.

| Patch | Fail ujian |
|---|---|
| 1. Reasoning `detailed` | `cli/normalize_test.go` (dah ada sebelum ni) |
| 2. Doom-loop | `conversation/stream_doomloop_test.go` |
| 3. Soft-session v4 | `gateway/soft_session_test.go` |
| 4+5. max_output 65536 | `inference/max_output_tokens_test.go`, `cli/max_output_tokens_test.go` |
| 6+7. Persona (+ berlapis) | `cli/persona_inject_test.go`, `config/persona_test.go` |
| 8. reasoning_opaque replay | `conversation/reasoning_replay_test.go` |
| 10. Hosted-tool diagnostic | `inference/hosted_tools_test.go` (12 ujian) |

### Diagnostik hosted-tool (patch #10, 22 Ogos)

**Punca:** akaun Grok Build Free tiada entitlement untuk server-side tools. Upstream **terima**
`web_search` / `code_interpreter` tanpa error, tapi **tak pernah jalankan** — kemudian model
karang jawapan sambil claim "aku dah search web". Disahkan live: `num_server_side_tools_used: 0`,
`num_sources_used: 0`, tiada `annotations`, dan jawapan tak konsisten antara panggilan.

**Kesan:** halusinasi senyap. Berbeza dengan reasoning-text yang hilang (upstream tutup CoT untuk
anti-distillation — model tetap fikir, cuma trace encrypted), ini **jawapan salah** tanpa amaran.

Bila request declare hosted tool tapi upstream tak jalankan:
- **Trailer HTTP:** `X-Grok2API-Warning: hosted_tool_not_executed; tools=web_search,x_search`
  (trailer, bukan header — buktinya hanya ada dalam `usage` di hujung response)
- **Log berstruktur:** `WARN hosted_tool_not_executed` + request_id, model, account_id, tools
- **Audit:** kolum `hosted_tool_warning`, nampak di admin UI (lencana amber) dan
  `GET /api/admin/v1/request-audits` (`hostedToolWarning`)

**Reka bentuk penting:**
- Hanya **7 tool server-side** dipantau (`web_search`, `x_search`, `code_interpreter`,
  `code_execution`, `image_generation`, `collections_search`, `file_search`).
  `function` / `shell` / `local_shell` / `mcp` / `bash_*` Anthropic **dikecualikan** — tools tu
  memang client execute, jadi `num_server_side_tools_used: 0` adalah **betul**. Kalau dimasukkan,
  setiap request Codex/Claude Code kena warning palsu.
- **`hosted_tool_warning` BUKAN `error_code`.** Request tu 2xx yang sah; guna `error_code` akan
  rosakkan `auditSuccessPredicate` dan kadar kejayaan dashboard. Disahkan pada DB: request
  yang diberi warning masih dikira `successful`.
- Empat syarat wajib elak false positive: ada tool declared, `usage.Reported` (response
  gagal/terputus takkan tuduh tools), `num_server_side_tools_used` **dan** `num_sources_used`
  dua-dua sifar, dan `OutputTokens > 0`.
- Alias berversi dinormalkan: `web_search_20250305` (Anthropic) dan
  `web_search_preview_2025_03_11` (OpenAI) → famili `web_search`.

**Jangan percaya `web_search` pada setup ni.** Untuk maklumat semasa, guna function calling dan
biar client fetch sendiri — itu jalan dan boleh dipercayai.

Migrasi DB automatik (AutoMigrate tambah kolum, default `''`). Sandaran pra-migrasi:
`../backups/backend.db.pre_hostedtool_*.bak`.

### Auto-retry tool-call degradasi (patch #11, 23 Ogos)

**Punca:** bukan bug gateway. Tingkah laku stokastik upstream — bila model kena hasilkan
argumen tool yang besar (dari ujian: ~400+ aksara), ~50% masa dia **rosot jadi narasi**
dan bukannya emit `tool_calls` berstruktur. Disahkan gateway 100% fidelity (hantar 2590
aksara argumen dalam history — model baca balik tepat). Punca ialah model, bukan pipe.

Bentuk degradasi yang diperhatikan (semua dari tangkapan live):
1. Prosa biasa: `run tool write_file with path is /tmp/x.md content is ...`
2. Kata kerja kosong: `call write_file with path is ...`, `invoke write_file ...`
3. Pseudo-XML berpagar: ` ```xml <tool_call name="write_file">... `, `<invoke tool="X">`,
   `<function call>`, `<parameter name="path">`

**Kesan sebelum patch:** log hermes penuh `Unrepairable tool_call arguments — replaced
with empty object`; command pendek (`pwd`) jalan, tapi `write_file`/`skill_manage patch`
dengan `new_string` panjang kerap gagal senyap.

**Reka bentuk — verdict berasingan `QualityToolDegraded`:**
- **Akaun TIDAK PERNAK dihukum.** Degradasi stokastik, bukan salah akaun. Tiada cooldown,
  tiada disable. (Berbeza dari `QualityWithhold` missing-thinking yang cool 12 jam.)
- **Sentiasa `deliver_last` bila retry habis** — jangan 503; narasi masih jawapan boleh baca.
- Bajet sendiri `toolDegradation.maxAttempts: 3` (default; 1-6). Kadar degradasi ~50%,
  3 percubaan ≈ pulihkan ~87%.
- Hanya request **streaming** yang declare **client-side tools** (`function`/`custom`).
  Hosted tools dikecualikan — replay boleh ulang search/sandbox upstream.
- Pengesanan dalam peek loop — narasi nampak awal (~40 aksara), tak tunggu stream tamat.
  Semakan degradasi mesti datang **sebelum** classifier missing-thinking `Deliver`
  (stub reasoning Grok datang dulu sebelum narasi — ini bug pertama kita semasa build).
- Dikesan semula dalam `finishQualityPeek` untuk stream yang tamat sebelum loop nampak.

**Config (`config.yaml`):**
```yaml
qualityGuard:
  requestRetry:
    toolDegradation:
      enabled: true   # matikan untuk off sepenuhnya
      maxAttempts: 3  # 5 untuk kadar pulih lebih tinggi (kos token naik)
```

**Ujian:** 20+ ujian termasuk semua bentuk live, hostile false-positive suite
("You can run bash yourself" mesti TIDAK dikesan), real tool call tak di-retry,
regresi stub-only hold, dan `DecideToolDegradationRetry` bounds.

**Bukti live:** `tool_degraded → retry (account 4) → retry (account 16) → deliver_last
(account 18)` — rotate akaun tanpa penalti. Kadar TOOL naik 2/6 → 4/6 pada tangkapan.

### Salvage XML → structured call (fasa 2, 23 Ogos)

Setelah retry berjalan, majoriti degradasi dilihat berbentuk **XML berpagar** yang
membawa argumen penuh — hanya formatnya salah. Daripada retry (kos generasi kedua),
gateway kini **parse narasi itu dan emit `tool_calls` berstruktur sintetik** terus.

**Reka bentuk (`gateway/tool_salvage.go`):**
- Hanya blok **berpagar** ` ```xml ... ``` ` (atau ditamatkan penutup tag berulang,
  corak degraded sebenar: `</parameter></parameter></parameter>`) — prosa biasa
  tiada sempadan boleh dipercayai, kekal pada laluan retry.
- Nama tool dari atribut wrapper (`<tool name=`, `<invoke tool=`, `<tool_call name=`),
  atau fallback ke tool tunggal yang dideklarasi. Nama tak dideklarasi = ditolak
  (tolak "delete_everything" halusinasi).
- Argumen dari pasangan `<parameter name="X">V</parameter>` (yang terakhir boleh tak
  tertutup — nilai lari ke fence) dan tag telanjang (`<path>V</path>` — mesti tertutup).
  Fence penutup guna `LastIndex` supaya ` ``` ` bersarang dalam kandungan tak dipotong.
- Nilai argumen **verbatim** (tiada unescape) — kandungan dengan `&&`, `%%s`, `{%}`
  kekal seperti asal.
- Keluaran: satu delta `tool_calls` penuh + bingkai `finish_reason=tool_calls` +
  `data: [DONE]`, dengan id `chatcmpl-salvage-*` / `call-salvage-*`.
- `salvageToolCallStream` sentiasa pulangkan reader yang boleh guna: gagal = replay
  byte asal, supaya `deliver_last` masih hantar narasi prosa.

**Perbezaan dari retry:** salvage **tiada kos token tambahan** dan lebih pantas
(tunggu narasi selesai, bukan jana semula). Retry kekal sandaran untuk degradasi prosa.

**Bukti live:** `native=12 salvaged=2 text=1` — gateway reconstruct 2 panggilan dari
narasi XML secara senyap. Kadar TOOL 10/10 → kekal; degrade prosa (tiada fence)
masih dapat dipulihkan oleh retry.

**Nota:** XML salvage (parse narasi jadi `tool_calls` sintetik) dicadangkan sebagai
fasa 2 — majoriti bentuk degradasi bawa argumen penuh, cuma format salah. Belum dibina.

### Placeholder reasoning bila CoT disorok (patch #12, 23 Ogos)

**Punca:** upstream (anti-distillation xAI) berhenti hantar teks CoT plaintext — hanya
`encrypted_content`. `usage.completion_tokens_details.reasoning_tokens` kekal tepat,
jadi kita tahu model fikir, tapi client nampak trace kosong.

**Fix:** bila `done*` tiba dengan `ReasoningTokens > 0` dan **tiada** trace pernah
sampai ke client (`reasoningEmitted` false, tiada blok thinking):
- Chat: delta `reasoning_content` `[thinking: N tokens — trace withheld by upstream]`
  sebelum frame finish
- Messages: blok thinking sintetik (start → thinking_delta → stop) sebelum `message_delta`

**Guard:** placeholder takkan keluar bila (a) trace sebenar pernah stream, (b) blok
thinking dah ada (walaupun signature-only), atau (c) `reasoning_tokens == 0`.
Quality-guard scanner tak terganggu — placeholder hanya dipancarkan pada `done*`,
selepas verdict peek; bukti encrypted_content sudah diterima scanner sebagai thinking.

**Status upstream berubah-ubah:** semasa ujian 23 Ogos, `grok-4.6-high` mula hantar
semula CoT plaintext. Placeholder hanya muncul bila upstream menyorok semula —
ia fallback, bukan override.

### ✅ DIBAIKI: persona dilangkau bila `system` ada tapi kosong

**Gejala:** request `/v1/messages` sampai ke upstream **tanpa arahan sama sekali** — bukan persona AKIF,
bukan arahan client. Jawapan jadi generik tanpa sebab yang jelas.

**Punca:** pintu persona tanya *"adakah field `system` wujud?"* dan bukan *"adakah ia ada isi?"*.
`isEmptyJSON` (`cli/normalize.go`) hanya kenal ``, `null`, `""` sebagai kosong. Field `system` Anthropic
boleh jadi string **atau** array blok, jadi 5 bentuk ini semua tersalah dikira "client dah bagi arahan":

`[]` · `"   "` · `"\n\t"` · `[{"type":"text","text":""}]` · `[{"type":"text","text":"  "}]`

**Pembetulan:** helper baru `hasAnthropicSystemContent()` dalam `cli/adapter.go`, dipakai dalam
`injectPersonaIntoMessagesRequest` **sahaja**.

**Sebab tak ubah `isEmptyJSON`:** helper tu dipakai **43 tempat dalam 9 fail** (`tools`, `include`, `input`,
`max_tokens`, `response_format`, ...). Menambah `[]` sebagai "kosong" di situ akan ubah tingkah laku
semua tempat tu sekali gus — contohnya `"tools": []` jadi "tiada tools". Blast radius helper baru: **1 fungsi**.

Ujian: `TestInjectPersonaIntoMessagesRequestFillsContentlessSystemField` (5 varian),
`TestHasAnthropicSystemContentTreatsUnknownShapesAsContent` (13 varian — termasuk blok imej dan
`cache_control` yang mesti dikira ada/tiada isi dengan betul).

### Persona berlapis: `systemPromptWhenClientHasSystem`

**Masalah:** `appendWhenClientHasSystem: true` menyebabkan persona penuh ditambah atas system prompt IDE.
Persona penuh ada arahan **WAJIB** (mesti ada emosi, mesti flag security, mesti sebut UX gap) yang berlawan
dengan arahan **format** IDE (balas diff sahaja, guna tool ikut spesifikasi). Model kena pilih satu →
gejala: jawapan berceloteh dalam konteks kod, abai peraturan diff.

**Penyelesaian:** dua persona berasingan dalam `config.yaml`:

| Situasi | Field digunakan | Saiz |
|---|---|---|
| Client tiada system prompt (Chatbox, curl) | `systemPrompt` | ~985 token |
| Client ada system prompt (opencode, Cursor) | `systemPromptWhenClientHasSystem` | ~130 token |

**Apa yang dibawa ke variant IDE:** standard kejuruteraan (OWASP/security, performance, accessibility,
error+loading+empty states, edge cases, trade-off jujur) **dan** suara.

**Apa yang sengaja ditinggalkan:** arahan wajib tona (*"setiap jawapan mesti ada emotional marker"*).
Arahan tu berlawan dengan arahan bentuk IDE, dan itu punca gejala berceloteh.

Sebab checklist penting: persona **tidak** memberi kemahiran — Grok memang dah tahu React/OWASP.
Checklist menetapkan **keutamaan**, iaitu apa yang diperiksa tanpa disuruh. Itu beza antara
"jawab soalan kau" dan "perasan ada SQL injection walaupun kau tanya pasal CSS".

⚠️ **Dua frasa penjaga dalam variant — jangan buang bila edit:**
`briefly` (pada flag risiko) dan `never the shape of your reply` (penghujung).
Dua-dua tu yang halang standard kejuruteraan bertukar jadi esei panjang.

**Fallback:** kosongkan field → guna `systemPrompt` (tingkah laku lama, tiada breaking change).

**Tool contract selamat:** diuji — `tools`, `tool_choice`, `parallel_tool_calls`, `model` kekal
byte-identical selepas persona diinject. Jadi risiko sebenar adalah percanggahan arahan, bukan kontrak tool.

⚠️ **Field baru = kena rebuild image.** Kalau tukar `config.yaml` sahaja tanpa rebuild, container akan
crash-loop dengan `field systemPromptWhenClientHasSystem not found in type config.PersonaConfig`.
Rebuild: `docker build -t grok2api:local-layered .` lalu set `GROK2API_IMAGE` dalam `.env`.

### Nota: alias effort hanya diwarisi kalau model sokong level tu

`grok-4.5-xhigh` **bukan** alias sah — grok-4.5 berhenti di `high`, hanya grok-4.6 ada `xhigh`.
Jadi ia jatuh ke heuristik 10% context, bukan warisi 64k. Ini betul, bukan bug.
Dipin dalam `TestModelMaxOutputTokensUnsupportedEffortSuffixIsNotAnAlias`.

### Patch mana upstream dah ada, mana masih eksklusif kita

Disemak pada 22 Ogos 2026 selepas merge `d6f6e9f5` — **semua patch di bawah masih eksklusif kita** dan terselamat dalam merge:

| Patch | Upstream ada? | Nota |
|---|---|---|
| Doom-loop (semua) | ❌ | `git grep -n "DoomLoop\|RepeatCount" origin/main` = kosong |
| reasoning_opaque | ❌ | `git grep -n "reasoning_opaque" origin/main` = kosong |
| Reasoning `detailed`, persona, max_tokens 65536 | ❌ | tak disentuh upstream |

Upstream kini ada sistem **reasoning evidence markers** (`grok2api-reasoning-start`/`-evidence` SSE comments)
untuk quality-guard — kita gabungkan dengan reasoning_opaque; dua-dua hidup berdampingan.
Video `mediaGenInput`, audit retention, account-bound proxy leases, dan requestRetry default baru
semua dah masuk melalui merge 22 Ogos.

**PR dihantar ke upstream:** [chenyme/grok2api#994](https://github.com/chenyme/grok2api/pull/994) — doom-loop split thresholds + ujian
(status: masih OPEN, tiada review lagi). Kalau diterima, buang patch #2 dari senarai.

### Benda baru dari merge 22 Ogos yang patut kau tahu

- **requestRetry aktif sekarang** (`qualityGuard.requestRetry.enabled: true`): jawapan tanpa thinking akan
  dicuba semula hingga 6 kali (hold 30s). Kalau rasa lambat untuk model bukan-thinking, boleh matikan.
- **audit retentionDays: 7** — log request lebih lama dari 7 hari dibuang automatik.
- **Compose** sekarang ada healthcheck eksplisit + log rotation (10m × 3) — pilihan aku, bukan upstream.
- **`make verify` / `tools/verify-patches.ps1`** — satu command verify semua patch selepas merge.
  Key disimpan dalam `tools/.verify-key.txt` (gitignored). Reveal semula kat admin UI kalau hilang:
  Client Keys → reveal secret.
- **`make clear-cooldown` / `tools/clear-cooldown.ps1`** — bulk clear cooldown seluruh pool.
  Lahir dari insiden 22 Ogos (client 185k-token retry storm bakar 116 akaun cooldown 12 jam).
  **Order penting: STOP client yang retry dulu, baru clear** — kalau tidak, setiap retry
  bakar 6 akaun baru setiap 30 saat dan kau kejar baldi bawah paip mengalir.
  Script ada warning automatik kalau ada trafik gagal dalam 2 minit terakhir.

### Playbook insiden "Respons upstream tiada penaakulan" (503)

Punca lazim: guard anti-降智 sejukkan akaun yang bagi jawapan tanpa reasoning.
Tiga punca biasa, ikut kekerapan:

1. **Client auto-retry storm** — client besar (context ~185k+) gagal → 503 retryable →
   auto-retry setiap 30 saat → setiap retry cuba 6 akaun → semuanya cooldown 12 jam.
   Gejala: cooldown naik cepat (16 → 116 dalam ~5 jam). **Fix: stop client, lepas tu
   `make clear-cooldown`.**
2. **Sesi client terlalu besar untuk free tier** — input 185k + xhigh = upstream balas kosong.
   Fix: sesi baru / compact. xhigh untuk tugas fokus (<50k context), high/medium untuk sesi panjang.
3. **Degradation upstream sebenar** — jika 503 berterusan walaupun tiada client besar, tunggu
   dan monitor; cooldown 12 jam akan expire sendiri.

Bukan-bukan yang patut kau tahu:
- Clear-cooldown UI admin kini ada **"Clear all" button** pada card Abnormal (muncul bila ada cooldown) —
  backend `POST /api/admin/v1/accounts/clear-cooldowns` (commit `40c88071`). `tools/clear-cooldown.ps1`
  masih berguna untuk CLI/scripting. Kedua-dua sama fungsi.
- Query admin API: **scan SEMUA page** (pool boleh tumbuh melebihi pageSize — insiden 22 Ogos:
  13 akaun tersembunyi kat page 2 sebab pool 297→339 sambil kita clear).
- Summary endpoint (`/accounts/summary`) ialah sumber betul untuk kiraan dashboard.

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
   $env:PATH = "C:\Program Files\Go\bin;$env:PATH"
   cd backend; go build ./...; go test ./...
   ```
2. **Rebuild image:**
   ```powershell
   docker build -t grok2api:local-nltools .
   docker compose up -d
   ```
3. **Verify patch masih ada — SATU COMMAND:**
   ```powershell
   powershell -ExecutionPolicy Bypass -File tools/verify-patches.ps1
   # atau offline-only: ... verify-patches.ps1 -SkipLive
   ```
   Ia check: patch markers (6), config drift vs example, live `/v1/models` limits
   (context=500000/output=65536), dan chat completion persona.
   - ✅ Persona: jawapan mula dengan emosi AKIF ("Wah...", "Aduh...")
   - ❌ Persona hilang: jawapan neutral → check `config.yaml` section `persona:` masih `enabled: true`

   **Kalau reveal key diperlukan:** admin UI → Client Keys → reveal secret → simpan dalam
   `tools/.verify-key.txt` (gitignored).

### Kalau benda rosak teruk (fallback)
```powershell
# Balik ke patch kita, buang merge
git merge --abort          # kalau masih dalam merge
git reset --hard backup-pre-merge-20260822   # keadaan pra-merge 22 Ogos (atau local-patches)
docker tag grok2api:backup-20260822 grok2api:local-nltools   # image pra-merge
docker compose up -d

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
