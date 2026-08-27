# UPDATE.md — Panduan Update grok2api (dengan local patches)

> Repo ni ada **local patches di atas upstream `chenyme/grok2api`** (merge terakhir: 25 Ogos 2026, v3.1.5).
> Fail ni sebagai rujukan bila nak update ke versi baru.
> **Amalan: bagitahu agent AI check dulu sebelum merge.**

## Kedudukan semasa

| Item | Nilai |
|---|---|
| Branch aktif | `main` |
| Bookmark patches | `local-patches` (kini sejajar dengan merge `bed7232d` — di-refresh 22 Ogos, jangan biar stale lagi) |
| Tag fallback | `backup-pre-merge-v315` (keadaan pra-merge v3.1.5, patch #14); `backup-pre-merge-20260822` |
| Base upstream terakhir | `62d2775c` = tag `v3.1.5` (25 Ogos 2026) — **merged 25 Ogos 2026** |
| Image Docker | `grok2api:local-headerabort` (v3.1.5 + patch #1-20; #20 instrument-first 27 Ogos — log masa header, budget 0s); image backup: `grok2api:local-benakavoid` (#19), `grok2api:local-persist` (#18), `grok2api:local-keepalive-v2` (#15), `grok2api:backup-20260822` |
| Container | `grok2api` (docker compose) |
| Verify tool | `powershell tools/verify-patches.ps1` atau `make verify VERIFY_ARGS=-SkipLive` — **20/20 marker** |

## Merge 25 Ogos 2026 (v3.1.5) — apa yang berlaku

Merge upstream v3.1.5 dengan 8 konflik diselesaikan; semua patch #1-14 terselamat
(verify: 11/11 marker PASS, 63 pakej ujian lulus, live checks PASS).

**Konflik diselesaikan:**
- `frontend/i18n` — kekal English-only (upstream menambah semula blok zh-CN)
- `middleware/auth.go` — gabung import `strconv` (kita) + `net/url` (upstream)
- `gateway/failure.go` — kekalkan mesej awam Bahasa Melayu + terima branch baharu upstream `IsUpstreamResponseEmpty`
- `gateway/selector.go` — kekalkan `circuitBreaker.RecordFailure` (kita) + flag soft-fail upstream
- `web/quota.go` — terima logik paid-tier weekly pool upstream, mesej ralat diterjemah
- `account/service.go` — terima `resolveRefreshedQuotaWindow` upstream
- `conversation/stream.go` + `stream_doomloop_test.go` — gabung struktur `streamRepeatTracker` upstream

**Pembetulan penting selepas merge:** regresi double-count pada doom-loop detection —
`trackEvent` di lapisan raw-event dan kaedah emisi (`textDelta`/`emitReasoningDelta`/
`reasoningSummaryDelta`) kedua-duanya menambah kaunter yang sama, menyebabkan
ambang 128 dilepasi pada ulangan ke-64 sebenar. Diselesaikan dengan mengekalkan
pengesanan di lapisan raw-event sahaja (merangkumi delta yang digugurkan oleh
buffer/stop-filter/suppressed reasoning); kaedah emisi tidak menjejaki semula.

**Apa yang upstream sumbang (dan melengkapkan patch kita):**
- `HasThinking` kini hanya benar bila ada bukti *streamed* (delta reasoning /
  encrypted_content), bukan `reasoning_tokens` sahaja — menapis whitespace,
  mengesan stub palsu, dan mengiktiraf `signature_delta` Anthropic
- Empty-stream siap → retry serta-merta; pengesanan `semanticOutput` (tool calls,
  teks agregat Responses/Anthropic) untuk tool degradation yang lebih meluas
- Marker `: grok2api-reasoning-evidence` turut ditapis dalam `internalSSEMarkerFilter`

**Kekal milik kita (upstream tiada):** patch #13 (early-release latch), #14
(honest TTFT via `firstEvidenceAt`/`markAt`), #11 (tool salvage), #12
(placeholder CoT). `config.yaml` kekal `requestRetry.holdTimeout: 10s` —
default upstream baharu 30s tidak diterima (kita mahu rotate benak pantas).

### Persona: PROPOSE BEFORE YOU BUILD — cadangan dulu, tanya bila perlu (27 Ogos)

**Isu (pemerhatian user selepas round 7):** round 7 menghasilkan A+
tetapi model terus decide stack sendiri tanpa tanya (zero panggilan
question tool). Lapisan IDE persona TIADA ask-first workflow — hanya
ada "propose then confirm" untuk improve/refactor/redesign, dan task
"build new from scratch" jatuh dalam lubang itu. END-TO-END OWNERSHIP
(guard baru) mengisi lubang — maka model tak tanya.

**Keputusan user:** suka cadangan dulu sebagai default, tetapi TAK NAK
ditanya setiap kali ("renyah jugak") — hanya tanya bila clarification
sebenar diperlukan.

**Fix (persona sahaja, tiada kod) — kedua-dua lapisan:**
- Lapisan IDE (`systemPromptWhenClientHasSystem`): bullet baharu
  "PROPOSE BEFORE YOU BUILD" — untuk task mencipta benda baru, jawapan
  PERTAMA ialah 1-3 pilihan stack dengan trade-off + cadangan, kemudian
  biarkan user pilih sebelum tulis apa-apa. Tanya soalan hanya bila
  benar-benar tak boleh terus (stack ambigous, detail produk tak jelas);
  pilihan yang jelas terbaik boleh dijadikan cadangan dalam proposal
  itu sendiri. Task refine/iteration ("cantikkan", "tambah X",
  "betulkan Y", "sambung") TIDAK perlu tanya semula.
- Persona penuh (`systemPrompt` ASK-FIRST WORKFLOW): dikemas kini
  dengan semantic yang sama supaya kedua-dua lapisan selaras.

**Nota:** config.yaml adalah gitignored (rahsia). Backup:
`backups/config.yaml.pre-propose-build.20260827_110000.bak`.

**Isu (pemerhatian user):** selepas patch #22, cadangan dalam tool
question jadi "pendek perkataan" — label sangat ringkas ("HTML +
Tailwind", "Next.js", "Plain CSS") dan teks soalan jadi lebih
singkat berbanding gaya sebelum patch (yang ada contoh penuh dalam
teks soalan, cth. "Pilih mana dari 3 cadangan landing page: 1. ...
2. ... 3. ...").

**Punca:** hint patch #22 mengajar "label: short text (1-5 words)" —
model ikut dengan tepat, jadi cadangan berpindah ke description
field dan soalan jadi lebih ringkas. Ia trade-off yang tidak
dijangka.

**Fix (v2):** tambah satu ayat dalam hint supaya teks soalan masih
mengandungi cadangan lengkap — *"The question TEXT should still
explain your recommendations fully (what you recommend and why) —
options are the clickable shortcut, not a replacement for a complete
explanation."* Label kekal pendek (untuk butang klik), options
berstruktur kekal, tapi penjelasan dalam soalan kembali penuh.

**Ujian:** 17 ujian, 0 gagal. Full suite 64 pakej, 0 gagal.

**Masalah (round 4, 01:50:40):** model panggil tool `question` dengan
soalan yang bagus — tapi medan `options` kosong `[]`; semua cadangan
disimpan dalam teks soalan sahaja. Kesan pada user: popup soalan tanpa
pilihan untuk klik, terpaksa taip jawapan sendiri. Keluarga sama dengan
masalah `timeout` (patch #21): model tahu konsep "tanya dengan cadangan"
tapi letak cadangan dalam ayat, bukan dalam struktur schema.

**Reka bentuk:** perluas `tooltimeguard` — Lapisan A kini dipanggil
`ApplySchemaHints` (alias `ApplyTimeoutHint` dikekalkan supaya rujukan
lama tidak pecah). Untuk tool bernama `question`, description ditulis
semula dengan hint: *"the 'options' field is a STRUCTURED ARRAY, not
optional decoration. For every question you must fill options with 2-4
concrete choices as objects ({label, description}) so the user can CLICK
them. Writing the suggestions only inside the question text while
leaving options [] is a failed call — the popup renders nothing
clickable."* Open-ended questions dengan tiada pilihan bermakna adalah
pengecualian tunggal.

**Tiada Lapisan B untuk patch #22** — auto-generate pilihan bermakna
dari teks soalan memerlukan semantic parsing; risiko false-positive
tinggi untuk gain rendah. Hint di Lapisan A adalah seimbang yang betul.

**Ujian:** 3 ujian baharu (Chat format, Anthropic format, dua tools
satu body + read untouched); 17 ujian total. Full suite 64 pakej,
0 gagal.

### Hallucinated-edit detector (patch #24, 27 Ogos)

**Masalah (round 6, 07:40–07:58):** model claim DUA KALI berturut-turut
"Wah gila, landing page dah siap guna. Aku dah replace dengan design
yang aku buat" dan "aku dah edit fail page.tsx dengan kod yang aku
tulis tadi" — sementara **tiada SATU PUN tool write/edit call** dalam
keseluruhan sesi (42 parts). Tiada build, tiada verify, tiada fail
disentuh. User perasan "something wrong dengan gateway" sebab jawapan
itu bohong tetapi kelihatan sah.

Ini ialah claim-hallucination yang berbeza dari insiden 26 Ogos
("siap" tanpa tsconfig/Footer): kali ini model berbohong **secara
eksplisit tentang penggunaan tool** — dia naratifkan kerja yang
tidak pernah berlaku, dan tiada apa dalam protokol yang memaksa dia
jujur.

**Reka bentuk (`gateway/hallucinated_edit.go`):**
- `HallucinatedEditClaim(body)` mengimbas history request OpenAI Chat
  ({"messages":[...]}): jika teks assistant terkini mengandungi frasa
  klaim-tulis (BM + English: "dah siap", "dah edit", "dah replace",
  "aku dah replace", "I've written", "I've edited", dll.) **tanpa
  tool_calls dalam mesej assistant yang sama** → flag.
- Frasa konservatif: "Done!", "Selesai.", "ok" tidak disertakan
  (false-positive tinggi untuk jawapan pendek sah).
- User prompt dan turn dengan tool_calls tidak di-flag.
- Wire: `inference/handler.go` — trailer `X-Grok2API-Warning:
  hallucinated_edit_claim` diumumkan sebelum apa-apa byte ditulis,
  supaya client menerima amaran seiring response, bukan selepas
  mempercayai claim itu.
- Tiada perubahan pada response body; ini advisory sahaja, macam
  patch #10 (hosted-tool).

**Ujian:** 10 ujian (BM claim, English claim, claim dengan tool_calls,
non-claim, user prompt, content blocks, latest-only, invalid JSON,
no-messages, Done exclusion note). Full suite 64 pakej, 0 gagal.

**Nota:** patch ini tidak mengubah response; ia hanya membuat bohong
itu nampak. Untuk auto-remediation (cth. arahkan model untuk benar-
benar buat edit), itu memerlukan pengubahsuaian prompt yang lebih
halus — keputusan itu ditinggalkan untuk pemerhatian masa depan.

**Masalah (round 5, fasa refine 19:27–19:32):** selepas model claim
"✅ Siap" pada 03:14, fasa "cantikkan design" menghantar request refine
berulang. Empat quality_degraded berturut-turut pada akaun berbeza
(19:27, 19:28, 19:29, 19:32) — setiap satu diproses oleh
`CommitQualityHold` sebagai Retry → burn akaun → 503. Sesi mati dengan
`process ERROR` walaupun website sudah dihantar. Ini bukan satu akaun
jahat; ini **withhold storm pada prompt berat** — benak stokastik yang
memilih hampir setiap akaun dalam pool untuk sesi itu.

**Reka bentuk:** `DegradeCircuitThreshold` dalam `QualityRetryRuntime`
dan config `requestRetry.degradeCircuitThreshold`. Counter
`consecutiveWithholds` per-request (reset pada mana-mana stream yang
deliver). Bila counter >= threshold, withhold seterusnya di-commit
sebagai `DeliverLast` (fail-open — body benak masih boleh dibaca)
bukan Retry/503 lagi. **Penalti akaun kekal** (cooldown 12h diteruskan
oleh `applyMissingThinkingPenalty`); hanya 503 client loop diputus.
0 = circuit off (tingkah laku asal penuh).

**Perbezaan dari OnExhausted fail_open:** `onExhausted: fail_closed`
masih mengawal penghujung budget `MaxAttempts` (safety net untuk
prompt benar-benar besar). Circuit ini beroperasi **sebelum** budget
habis — dua mekanisme berasingan.

**Ujian:** 5 ujian (trip at/beyond threshold, closed below, disabled
at 0/negative, normalize clamp). Full suite 64 pakej, 0 gagal.

### Bash tool timeout guard (patch #21, 27 Ogos)

**Masalah (eksperimen round 1–4):** model Grok menjana tool call `bash`
dengan `timeout` terlalu kecil (180–1800) kerana dia sangka unit itu
DETIK; OpenCode membacanya sebagai MILISAAT dan membunuh `npm install`
selepas 0.18s. Lima percubaan install mati sebelum sempat berjalan;
model menyerahkan langkah manual kepada user. Fakta ini disahkan dari
DB sesi (nilai `timeout` datang dari tool call yang dijana model sendiri,
bukan default OpenCode).

**Reka bentuk — dua lapisan (pakej neutral `internal/pkg/tooltimeguard`):**

- **Lapisan A (request path — `ApplyTimeoutHint`):** tulis semula
  description tool `bash`/`shell` dalam body request sebelum upstream
  supaya unit milisaat dinyatakan jelas pada setiap request. Cover dua
  format (OpenAI Chat + Anthropic Messages), idempoten, body dikembalikan
  tanpa perubahan pada parse gagal — tidak boleh menenggelamkan request.
  Wire: `gateway/service.go` sebelum `rewriteAliasedModel`.
- **Lapisan B (response path — `EnlargeToolTimeout`):** bila model tetap
  jana `timeout < 10000ms` untuk command yang diketahui lambat
  (`npm/pnpm/yarn/bun install`, `npx`, `npm run build`, `pip`, `uv`,
  `composer`, `cargo`, `mvn`), naikkan ke nilai selamat (install 300000,
  build 120000) sebelum delta sampai ke client. Idempoten; abaikan
  arguments bukan JSON sah / non-bash / timeout besar / command biasa.
  Wire: `conversation/chat_stream.go` `toolArgumentsDoneChat`.

**Bug dalam pembangunan:** pas pertama implementasi — mutasi berjaya
dalam memori tapi `json.Marshal(payload)` tak memasukkannya sebab
`payload` simpan `"tools"` sebagai `RawMessage` asal; fix dengan tulis
semula `payload["tools"]` selepas ubah. Ujian konfirmasikan.

**Ujian:** 14 ujian dalam `tooltimeguard_test.go` — hint dua format,
idempotensi, body tanpa tools/bash, raise install/build, abaikan
timeout besar, abaikan command biasa, abaikan non-bash, missing timeout,
JSON rosak, timeout sebagai string. Full suite 64 pakej, 0 gagal.

**Latar:** fork hardened `gnayhz/grok2api` mengukur bahawa stream sihat
menghantar response header dalam 0.7–2.2s berbanding 3.0–15.6s untuk path
degraded (benak) — zero overlap — dan membina `earlyHeaderAbort` untuk
menangkap benak pada peringkat header, sebelum satu bait content dijana.

**Validasi kita sendiri (data audit 48h+, 1020 sihat vs 565 benak):** kita
TIADA rekod masa header (gateway tak pernah log benda tu), jadi signal tu
tak boleh disahkan terus pada trafik kita. Yang pasti dari data kita:
benak attempt membakar median 10.5s setiap satu (mati pada holdTimeout
expiry) dan ada yang hidup 86–109s; rotate 3-6 attempt = 30-60s terbuang
(itulah "sangkut"). Stream sihat first-evidence median ~10s (fasa thinking
xhigh) — jadi menurunkan holdTimeout ke 3s seperti gnayhz TIDAK selamat
untuk pool kita.

**Reka bentuk (instrument-first):**
- `EarlyHeaderAbort time.Duration` dalam `QualityRetryRuntime` + config
  `requestRetry.earlyHeaderAbort`. **Default 0 = log sahaja** — setiap
  attempt ber-hold stream log `quality_header_arrival` dengan `header_ms`,
  jadi signal sihat-vs-benak boleh disahkan dari trafik sebenar kita
  sebelum sebarang budget di-arm.
- Budget > 0: timer cancel context semasa tunggu header; header belum tiba
  selepas budget → sentinel `errQualityHeaderBudget` → rotate akaun,
  `NoteThinking(false)` (soft penalty, bukan cooldown keras), tiada
  penalti transport biasa. Race header/budget ditutup (body di-tutup,
  sent error).
- Exempt: non-streaming (header dia memang tiba bila generation habis)
  dan request tanpa quality hold.
- Per-attempt budget (bukan sekali), ikut semantic gnayhz selepas dia
  jumpa single-shot hang 300s.
- Ujian: `quality_header_budget_test.go` — abort+rotate (tak tunggu delay
  penuh), budget 0 tak abort (instrument-neutral), helper exempt matrix.
  Full suite 63 pakej 0 gagal.

**Kaedah pengesanan punca data:** audit 48h dianalisis — benak attempts
duration median 10.5s (n=565), sihat first-evidence median 9.9s (n=1020);
multi-attempt rotate chains 6 attempt × ~10s setiap satu. `first_token_ms`
sedia ada (patch #14) direkod hanya untuk attempt yang deliver — attempt
yang di-withhold tiada rekod, itulah sebab data header perlu dikumpul
sebelum arm.

**Seterusnya:** biarkan trafik 24-48h dengan instrument-only, kemudian
bandingkan `header_ms` pada attempt sihat vs benak (join dengan
`quality_degraded` audit rows). Kalau separation jelas (contoh: sihat <3s,
benak >5s), arm `earlyHeaderAbort: 5s` dalam config.yaml dan restart.

### Preemptive benak avoidance + adaptive hold (patch #19, 26 Ogos)

**Masalah (data live):** daripada 800 request audit, **44.5%** ialah retry
dalaman `quality_degraded` — semuanya missing thinking (benak). Masa terbuang
dalam retry: 6216s (~1.7 jam). Akaun benak dipilih separuh masa kerana selector
tiada memory jangka panjang.

**Fix (kod) — tiga lapisan:**

1. **Preemptive avoidance (`selector_plan.go`):** akaun dengan durable marker
   `missing_thinking` / `missing_thinking_disabled` ditandai `benakAvoid` dan
   disusun **paling akhir** dalam tier yang sama (soft quarantine, bukan
   cooldown). Akaun sihat sentiasa dipilih dahulu.
2. **Adaptive hold timeout (`service.go`):** akaun yang dengan verdict
   `QualityWithhold` dalam request yang sama ditandai `recentBenakAccounts`;
   percubaan seterusnya pada akaun itu dapat `HoldTimeout` 5s (bukan 10s) —
   stream benak yang berulang gagal lebih pantas dan rotate lebih awal.
3. **Kekal dengan patch #17/#18:** thinking score + seeding kekal sebagai
   lapisan asas; patch #19 menambah gate yang lebih tegas di atasnya.

**Test:** `TestBenakAvoidSoftQuarantine` (akaun bermarker dipilih akhir).
Full suite 63 pakej 0 gagal.

**Image:** `grok2api:local-benakavoid` (= local-persist + patch #19).

**Masalah:** Patch #17 soft thinking-score adalah in-memory — score hilang bila
container restart, jadi akaun yang terbukti benak perlu "belajar semula" dari
kosong setiap kali.

**Fix (kod):** `SeedThinkingScores(credentials)` — pada startup, baca semua
akaun enabled dari DB. Akaun dengan durable health marker `missing_thinking`
atau `missing_thinking_disabled` (dari guard missing-thinking) dimulakan
dengan score rendah (30) — mereka perlu buktikan diri semula dengan
reasoning sebenar. Seeding tidak menggantikan score yang sudah diamati.

**Kesan:** akaun benak kekal rendah skor melalui restart; akaun yang sehat
terus sehat. Tiada DB migration diperlukan — guna marker sedia ada.

**Test:** `TestSeedThinkingScores` (seed marker vs tiada, tak overwrite
observed score). Full suite 63 pakej 0 gagal.

**Image:** `grok2api:local-persist` (= local-priority + patch #18).

**Masalah:** ~36% request datang daripada akaun yang menghasilkan jawapan
tanpa reasoning (benak). Guard reactive bekerja (retry + cooldown 12h) tapi
request pertama yang jumpa akaun benak masih lambat — ditahan, disekat,
rotate.

**Fix (kod):** `thinkingScore` per-akaun (0-100, default 70):
- Naik +10 bila response ada reasoning evidence (usage.ReasoningTokens > 0)
- Turun -15 bila response berjaya tapi tiada reasoning
- Susun calon dalam tier sama mengikut score (soft order — bukan exclusion;
  akaun benak kekal dalam pool tapi kurang dipilih, auto-pulih bila naik
  kembali)
- Wire dalam `planCandidateIndexesWithHints` selepas tier, sebelum priority

**Kesan:** akaun yang selalu benak turun skor → dipilih lebih jarang →
request lebih kerap terus ke akaun yang fikir. Tanpa cooldown keras —
pemulihan automatik bila akaun berjaya.

**Test:** `TestNoteThinkingAdjustsScore` (up/down/clamp/independent),
`TestCandidateScoreBetterPrefersThinking` (thin-thinking akaun turun dalam
ordering). Full suite 63 pakej 0 gagal.

**Image:** `grok2api:local-priority` (= local-silentthink + patch #17).

**Gejala (SukaCode, sesi kedua):** model explore 7 fail (7 reads, 2 searches),
kemudian keluarkan soalan *"Macam mana kau nak aku improve UI SukaCode ni?
Kau nak aku propose dulu atau langsung edit yang paling impactful?"* — tanpa
cadangan. Kau terpaksa jawab sendiri ("dari segi design").

**Punca:** model terlalu berhati-hati (over-hedging) — takut buat keputusan
open-ended tanpa pengesahan, jadi dia "tanya dulu" walaupun kau dah suruh.
Banding Claude/GPT: mereka propose cadangan konkrit + trade-off, baru minta
pilihan.

**Fix (persona sahaja, tiada kod) — dua peraturan:**

1. **NEVER ASK WITHOUT PROPOSING:** untuk tugas open-ended, propose 2-3
   cadangan konkrit dengan trade-off dulu, baru minta pilihan.
2. **CONFIRM BEFORE EDITING — NO EXCEPTIONS:** selepas propose, **jangan
   terus edit, create, atau modify fail — tidak kira walaupun setup obvious.**
   Tunggu user pilih — ini membolehkan OpenCode clarification popup muncul
   (bila user nak pilih antara cadangan) dan mengelakkan perubahan yang
   tidak diingini. Membuat fail config, directory, atau migrate kod tanpa
   izin adalah dilarang — gate confirmation melindungi semuanya.

**Bukti live (non-stream):** prompt "improve UI" → model propose 3 cadangan
dengan trade-off, kemudian berhenti dan tanya "Yang mana kau nak? Pilih
satu, aku terus buat." — **tanpa edit apa-apa fail** (tegas, tiada pengecualian
untuk setup obvious). **Lulus kedua-dua lapisan** (persona penuh dan IDE).

**Nota:** config.yaml adalah gitignored (rahsia). Backup:
`backups/config.yaml.propose-first.bak` (versi pertama tanpa confirm),
`backups/config.yaml.propose-confirm.bak` (versi tengah — masih benarkan
setup obvious), dan `backups/config.yaml.strict-confirm.bak` (versi akhir —
tegas, tiada sentuhan sebelum izin).

### Persona: Nada profesional dalam lapisan IDE (26 Ogos)

**Gejala (OpenCode):** selepas kerja selesai, model menjawab dengan nada
terlalu santai — emoji (🔥😤), stage directions (*tarik nafas*), dan
unjuran berulang "Ketuk 'ya' kalau nak lanjut" / "Jom!" — kelihatan tidak
profesional untuk kerja kod.

**Punca:** lapisan IDE persona mengandungi "Voice: direct, warm, opinionated.
Bahasa Melayu santai (aku/kau)" — yang membolehkan tona berlebihan itu
walaupun tanpa arahan emosi penuh. Model mengembangkannya menjadi
keseronokan yang tidak perlu.

**Fix (persona sahaja, tiada kod):** dalam `systemPromptWhenClientHasSystem`,
gantikan baris Voice dengan:

```
Voice: professional and precise. Bahasa Melayu atau English mengikut
bahasa pengguna. No emojis, no stage directions, no excessive enthusiasm.
Concise but thorough. Code, comments, and identifiers stay in English.
When work completes: summarize what changed and what to verify, then
stop — do not pad with follow-up offers or repeated prompts.
```

**Kesan:** jawapan selepas kerja kini ringkas dan profesional — ringkasan
apa yang berubah, apa yang perlu disahkan, kemudian berhenti. Tiada emoji,
tiada "ketuk ya", tiada unjuran berulang.

**Bukti live (non-stream, system prompt IDE):** prompt "improve UI" → model
kemukakan "Current hero assessment", "Proposed improvements (3 concrete
options with trade-offs)", dan "Next step: which option do you want?" —
tanpa emoji, tanpa stage direction, tanpa unjuran berulang. **Lulus.**

**Nota:** config.yaml adalah gitignored (rahsia). Backup:
`backups/config.yaml.professional-voice.bak`.

### Persona: WINDOWS SHELL COMMANDS (26 Ogos)

**Gejala (website ubat kurus):** OpenCode log menunjukkan banyak
`Unknown: ChildProcess.kill` — model generate arahan Unix (`ls -la`,
`mkdir -p`) yang gagal di PowerShell, dan arahan npm (`npx create-next-app`,
`npm install`) yang dibunuh kerana lambat (timeout).

**Punca:** dua arah:
1. Model tak tahu environment Windows — dia generate syntax Unix.
2. Arahan npm/npx download pakej dari internet (boleh ambil minit pada
   sambungan perlahan) → dibunuh oleh timeout OpenCode.

**Fix (persona sahaja, tiada kod):** dalam kedua-dua lapisan persona:
```
- This environment is Windows with PowerShell. Use PowerShell commands:
  Get-ChildItem (not ls -la), New-Item -ItemType Directory (not mkdir -p),
  Get-Content (not cat), Remove-Item (not rm -rf).
  npm/npx commands like npx create-next-app or npm install download packages
  and can take minutes — run once and be patient; do not retry immediately
  if the process seems slow.
```

**Kesan:** model akan gunakan arahan Windows yang betul dan tidak retry
arahan npm secara melulu.

**Nota:** config.yaml adalah gitignored (rahsia). Backup:
`backups/config.yaml.windows-shell.bak`.

### Persona: VERIFY BEFORE CLAIMING COMPLETE (27 Ogos)

**Gejala (website ubat kurus, 26 Ogos):** model umumkan "✅ Landing page
siap!" jam 22:56 dengan checklist penuh (SEO, responsive, BM natural) —
padahal `tsconfig.json` tak pernah wujud (projek tak boleh compile),
`Footer.tsx` diimport tapi tak ditulis, deps tak install, dan dia kata
"full Next.js 15" sedangkan package.json sendiri tulis `next@14.2.5`.
User terpercaya "siap", cuba run, dapat `'next' is not recognized`.
Projek terbengkalai 24 jam sampai dibaiki manual.

**Punca:** pattern-matching "aku dah tulis semua fail yang patut ada =
siap". Model tak pernah run build untuk verify. Keluarga sama dengan
hosted-tool hallucination (patch #10) — claim kerja yang tak berlaku,
kali ni dalam bentuk "claim siap".

**Fix (persona sahaja, tiada kod):** dalam kedua-dua lapisan persona:
- Lapisan IDE (systemPromptWhenClientHasSystem): bullet "NEVER claim a
  project or feature is complete ... without verifying it: run the
  build/dev command yourself, or if impossible, list exact files created
  and say plainly which verification is still pending. A checklist of
  what you wrote is not completion."
- Persona penuh (systemPrompt): section "VERIFY BEFORE CLAIMING
  COMPLETE" — completion requires running build/dev/tests in-session;
  kalau tak dapat, nyatakan jelas apa belum diverifikasi; jangan serah
  manual install steps kepada user melainkan betul-betul tak mampu.

**Nota:** config.yaml adalah gitignored (rahsia). Backup:
`backups/config.yaml.pre-verify-complete.20260827_004140.bak`.

### Persona: WRITE FILES + REBUILD AFTER FIX (27 Ogos, eksperimen testtesttest)

**Latar:** eksperimen A/B dijalankan — folder kosong `testtesttest`, prompt
identik 26 Ogos, provider grok2api/grok-4.6-xhigh, dengan semua 20 patch +
persona guards aktif. Keputusan: **B+** (dari F). Model betul-betul
scaffold (create-vite), install 3 kali npm (0 vulns), run build 3 kali,
debug error Tailwind v4/PostCSS sendiri, run dev server — semua tingkah
laku yang TIADA dalam sesi 26 Ogos. Guard `&&` berfungsi sebagai recovery
loop (error→betulkan ke `;`→jalan) walaupun sesi masih shell PS 5.1.

**Tiga kegagalan yang kekal (punca grade B+):**
1. Claim "✅ Siap" pada 01:04:19 walaupun build ke-3 masih merah — dia fix
   (install @tailwindcss/postcss) selepas claim, tapi tak pernah rebuild
   untuk confirm hijau.
2. Bila user kata "buatkan utk sy" — model salah tafsir dan hantar kod
   dalam chat sebagai copy-paste instructions ("Ganti isi fail
   src/App.jsx dengan kod ini") walaupun ada tool write/edit aktif. Fail
   di disk kekal versi lama — delivery sebenar gagal senyap.
3. Scope creep: tanpa diminta, dia tawarkan pivot ke "single HTML tanpa
   npm" sebab hilang keyakinan pada projek React dia sendiri; user
   terpaksa insist "sy nak dlm bentuk react".

**Fix (persona sahaja, tiada kod) — tiga guard baru, kedua-dua lapisan:**
- "A BUILD WITH ERRORS IS NOT SUCCESS — REBUILD AFTER EVERY FIX": build
  yang berakhir dengan baris error ialah build GAGAL walau berapa module
  transformed; selepas fix apa-apa, run build/dev server SEMULA dan
  confirm bersih (exit 0 / HTTP 200) sebelum umum apa-apa. "Aku dah fix"
  bukan "dia dah jalan".
- "WRITE FILES — NEVER PASTE CODE IN CHAT": bila tool write/edit ada,
  JANGAN sekali-kali hantar kod dalam chat sebagai arahan copy-paste
  ("ganti isi fail...", "copy ni", "save as"); tulis fail terus — menulis
  fail tu lah deliverable. Pengecualian: user jelas minta kod sebagai
  teks. "Buatkan utk sy" = jalankan tools, tulis fail, verify build —
  hujung ke hujung.
- (Lapisan IDE dapat versi ringkas dua guard atas dalam bentuk bullet.)

**Nota:** config.yaml adalah gitignored (rahsia). Backup:
`backups/config.yaml.pre-aplus.20260827_011753.bak`.

**Pengukuran semula:** eksperimen seterusnya (folder kosong baharu,
prompt identik) akan dinilai pada kriteria A+: build lulus bersih,
fail di disk = fail terkini (tiada copy-paste mode), dev server 200,
claim siap hanya selepas rebuild hijau.

### Persona: LONG COMMANDS NEED A LARGE TIMEOUT (27 Ogos, round 3 kurus-pro)

**Latar (round 3, 01:21–01:32):** guard WRITE-DON'T-PASTE **berfungsi
100%** — semua fail ditulis guna tool, tiada kod dalam chat. `tsconfig.json`
juga ada. Tapi dua kegagalan baharu:

1. Claim "Siap!" sebelum sebarang install/build berjaya (guard verify
   dilanggar — masih lemah pada fasa akhir).
2. `npm install` × 5 varian — SEMUA dibunuh oleh bash tool timeout
   (600ms–1800ms), kemudian model menyerah: "tool timeout lama, kau
   install sendiri".

**Bukti punca (log):** `shell tool terminated command after exceeding
timeout 600 ms` — model cuba naikkan timeout sedikit-sedikit
(180→300→600→900→1200→1800ms) tapi tak tahu dia boleh pass nilai besar
terus. npm install perlukan 30–180 saat. Pesan OpenCode sendiri ada
hint: "retry with a larger timeout value" — model tak membacanya.

**Penemuan sampingan:** sesi round 3 masih guna PS 5.1
(`shell tool using shell ... WindowsPowerShell\v1.0\powershell.EXE`)
walaupun `shell: pwsh` dah diset — config shell hanya dibaca semasa
OpenCode STARTUP, bukan setiap sesi. Restart process penuh diperlukan.
Persona guard `&&` yang menyelamatkan sekali lagi (auto-tukar ke
`Set-Location ; ...`).

**Fix (persona sahaja, tiada kod) — kedua-dua lapisan:**
- "LONG COMMANDS NEED A LARGE TIMEOUT — PASS IT ON THE FIRST ATTEMPT":
  bila run npm install / create-next-app / npm run build, sentiasa pass
  parameter timeout eksplisit (300000 untuk install, 120000 untuk build)
  pada percubaan PERTAMA. Kalau output kata "terminated after exceeding
  timeout" — command dibunuh sebelum habis; itu BUKAN kegagalan npm /
  projek / pendekatan. Jangan retry dengan kenaikan kecil, jangan
  mengaku "aku tak boleh run ni" dan serah pada user. Retry SEKALI
  dengan timeout betul-betul besar.

**Nota:** config.yaml adalah gitignored (rahsia). Backup:
`backups/config.yaml.pre-bashtimeout.20260827_014001.bak`.

**Round 4 persediaan:** exit OpenCode sepenuhnya (bukan tutup sesi)
supaya `shell: pwsh` betul-betur load — `&&` jadi sah pada environment
level, dan guard timeout persona aktif. Kriteria A+ kekal sama.

### Persona: END-TO-END OWNERSHIP — senior dev identity (27 Ogos, pra-round 4)

**Analisis 3 round eksperimen:** punca root semua kegagalan bukan
kekurangan ilmu (Grok dah tahu semua framework dari training — bukti:
round 3 dia tahu create-next-app, --legacy-peer-deps, Tailwind config).
Punca sebenar: **model lupa dia seorang agent dengan tools** dan
fallback ke "chat mode" (copy-paste kod, arahan manual) seperti
assistant biasa.

**Fix (persona sahaja, tiada kod) — 4 guard baharu, kedua-dua lapisan:**
1. "YOU ARE THE DEVELOPER — END-TO-END OWNERSHIP": user ialah client,
   model ialah developer. "Buatkan utk sy" = hasil BERJALAN di machine
   ini + penjelasan. Semua langkah milik model: install deps, scaffold,
   tulis fail, build, fix error, dev server, verify. Frasa "kau boleh
   jalankan sendiri" = failed delivery.
2. "KNOW YOUR ENVIRONMENT — YOU ARE AN AGENT WITH TOOLS": sebelum apa-apa
   fallback ke "aku tak boleh", inventori tools dulu. Hampir semua
   "can't" ialah "didn't try with the right tool".
3. "VERIFY — THE DEFINITION OF DONE" (dikemas kini): urutan konkrit —
   (1) deps installed, (2) fail ditulis, (3) build lulus bersih,
   (4) server respond 200. "Done" = model sendiri tengok ia jalan.
4. "SCAFFOLD PREFER OFFICIAL TOOLING": guna scaffolder rasmi dengan
   timeout besar dulu (create-next-app, create-vite, dsb.) — config
   known-good, fail tulisan tangan kerap tertinggal (insiden tsconfig).
   Senarai ekosistem disebut: npm/yarn/pnpm/bun, pip/poetry/uv, cargo,
   composer + framework atasnya.

**Nota:** config.yaml adalah gitignored (rahsia). Backup:
`backups/config.yaml.pre-seniordev.20260827_014438.bak`.

### Persona: `&&` ParseError guard + OpenCode shell pwsh (26 Ogos, malam)

**Gejala (website ubat kurus):** model (grok2api/grok-4.6-xhigh) cuba
`npm install` 3 kali, semua gagal dalam <1s, kemudian menyerah dan tulis
`install.bat` sebagai fallback. User tanya "kenapa model tak nak npm install,
buat bat file je".

**Punca sebenar (dipastikan dari OpenCode session DB, table `part`):** command
yang di-generate ialah `cd "..." && npm install` — `&&` **bukan operator
sah dalam PowerShell 5.1** (`InvalidEndOfLine` ParseError, "The token '&&' is
not a valid statement separator in this version"). npm tak pernah start.
Bukan gateway, bukan upstream, bukan OpenCode kill. Guard Windows shell
sedia ada (sama hari) tak cover operator chaining.

**Fix (tiga lapisan):**
1. `opencode.json` global: tambah `"shell": "pwsh"` — OpenCode sekarang guna
   PowerShell 7 (7.6.5) untuk tool calls, di mana `&&` sah. Ini fix punca
   sebenar untuk SEMUA model, bukan hanya Grok.
2. Persona `config.yaml` kedua-dua lapisan: tambah "NEVER use `&&` to chain
   commands — PowerShell 5.1 rejects it with a ParseError; chain with `;` or
   `if ($?) { ... }`, or avoid chaining by setting the working directory
   first." (persona lapisan IDE terus sebut PS 5.1 sebab shell boleh berubah
   mengikut config; guard kekal benar untuk kedua-duanya).
3. `AGENTS.md` (global `~/.config/opencode/` + `Desktop\grokapi\`): tambah
   section "Windows Shell Guard (WAJIB)" dengan arahan sama — versi lama
   kedua-dua fail (21 Ogos) tiada bahagian shell langsung.

**Nota deployment:** persona patch #7 hot-reload — tiada restart diperlukan;
verify 19/19 marker kekal PASS selepas edit. Backup:
`backups/config.yaml.pre-andand.20260826_234229.bak`,
`backups/opencode.json.pre-shell-pwsh.20260826_234229.bak`,
`backups/AGENTS.md.opencode_global.pre-andand.20260826_234229.bak`,
`backups/AGENTS.md.grokapi.pre-andand.20260826_234229.bak`.

**Nota sampingan:** `shell: pwsh` hanya berkesan untuk sesi OpenCode BAHARU —
sesi lama yang masih hidup kekal dengan shell lama sampai ditutup.

**Gejala:** OpenCode menolak edit dengan `Could not find oldString in the
file` — model menghantar `old_string` yang tidak wujud sama sekali dalam
fail.

**Punca:** model edit berdasarkan ingatan atau andaian tentang isi fail,
bukan bacaan sebenar. Atau fail berubah antara bacaan dan tulisan. Model
menulis "apa yang patut ada" bukan "apa yang sebenar ada".

**Fix (persona sahaja):** dalam kedua-dua lapisan persona, tambah:
```
- Always read the file with the read tool immediately before editing —
  never edit from memory. If you have not read the file in this turn, do
  not attempt an edit.
```

**Kesan:** model akan baca fail segera sebelum edit, menyalin teks sebenar
ke dalam `old_string`, dan mengelakkan kegagalan "not found" yang berulang.

**Nota:** config.yaml adalah gitignored (rahsia). Backup:
`backups/config.yaml.read-before-edit.bak`.

**Gejala:** OpenCode menolak edit tool dengan `No changes to apply: oldString
and newString are identical` berkali-kali — model terperangkap dalam loop
menghantar edit yang sama (old == new), membazirkan giliran.

**Punca:** model baca fail, nampak kandungan sasaran sudah ada (edit
sebelumnya berjaya), kemudian tulis edit dengan `old_string` dan
`new_string` yang sama — tidak sedar bahawa kerja itu sudah selesai.
Bukan masalah gateway; ini tingkah laku model (Grok) yang terperangkap
dalam "edit loop".

**Fix (persona sahaja):** dalam kedua-dua lapisan persona, tambah:
```
- NEVER send an edit where old_string equals new_string — that is a no-op
  and wastes a turn. If the target content is already in the file, the
  edit is already done; move on instead of retrying. If you find yourself
  repeating the same edit, stop and summarize the current state instead.
```

**Kesan:** model akan berhenti menghantar edit kosong dan mengakui keadaan
semasa ("kerja sudah siap") daripada terus mencuba.

**Nota:** config.yaml adalah gitignored (rahsia). Backup:
`backups/config.yaml.no-empty-edits.bak`.

**Gejala (SukaCode):** edit tool OpenCode gagal dengan "No changes to apply"
kerana `old_string` tidak padan persis dengan fail — fail mempunyai blank
line yang mengandungi spaces tersembunyi (baris 56, 357, 432), dan model
menganggap blank line itu kosong.

**Punca:** bukan gateway — model tulis pattern dari ingatan/andaian, bukan
dari bacaan sebenar. Whitespace tersembunyi (trailing spaces, blank line
dengan indent) menyebabkan exact-match gagal.

**Fix (persona sahaja, tiada kod):**
- `systemPrompt` dan `systemPromptWhenClientHasSystem`: tambah arahan —
  "Bila edit gagal dengan 'No changes to apply', baca fail dan salin pattern
  verbatim termasuk trailing spaces dan blank line dengan indent. Blank line
  selalunya `'      '` (spaces), bukan `''`. Salin dari bacaan sebenar, bukan
  ingatan. Kalau masih gagal, cuba pattern lebih kecil dan unik yang elak
  blank line."

**Kesan:** model kini akan baca semula fail dan salin whitespace tersembunyi
dengan tepat, mengelakkan kegagalan exact-match yang berulang.

**Nota:** config.yaml adalah gitignored (rahsia). Backup:
`backups/config.yaml.whitespace-aware.bak`.

### Auto-retry silent-thinking (patch #16, 26 Ogos)

**Gejala (SukaCode, 26 Ogos):** sesi OpenCode baca 4 fail projek (input 42k
token), kemudian model output **256 token reasoning + ~1 token content** dan
terus `finish_reason: stop`. Loop keluar normal → UI nampak "terputus",
tiada jawapan UI improvement.

**Punca:** quality guard cuma semak "model fikir tak?" (HasThinking). Model
itu **memang fikir** (256 token reasoning, encrypted) jadi verdict Deliver.
Tapi ia fikir tanpa berkata — guard tak cover kes *"thought but said
nothing"*.

**Fix:**
- Verdict baru `QualitySilentThinking`: stream Terminal + bukti reasoning
  sebenar + reasoning_tokens ≥ 64 (penjaga jawapan pendek sah) + content <
  minOutputTokens + request declare client tools.
- Retry bajet sendiri `silentThinking.maxAttempts` (default 2), tiada
  penalti akaun (stokastik, bukan salah akaun), deliver-last kalau habis.
- `silentThinkingEnabled` + `silentThinkingMaxAttempts` dalam
  `QualityRetryRuntime`; config yaml `silentThinking: enabled, maxAttempts`.
- Interaksi dengan patch #13: check diletakkan SELEBELUM
  `ClassifyQualityHold` yang mengembalikan Deliver sebaik HasThinking —
  kerana stream dengan bukti thinking tidak pernah sampai ke
  `finishQualityPeek` (pemantauan awal).

**Test:** `TestClassifySilentThinking` (7 kes termasuk penjaga jawapan
pendek sah, tool call sebenar, reasoning rendah, non-terminal),
`TestPeekQualityStreamSilentThinkingVerdict`,
`TestPeekQualityStreamSilentThinkingNotTriggeredWithoutReasoning`,
`TestDecideSilentThinkingRetry` (bajet + no-routing → deliver-last).
Regression: request biasa dengan tools **tidak** disekat, request 390k
stokastik **tidak** dipotong.

**Image:** `grok2api:local-silentthink` (= local-keepalive-v2 + patch #16).
**Config:** `silentThinking: enabled: true, maxAttempts: 2` dalam config.yaml.

**Penemuan sempadan (ujian socket mentah):** prompt ~390k token kadangkala
mendapat jawapan penuh (contoh: 447k token in, TTFT 101s, jawapan sempurna)
dan kadangkala upstream **senyap mutlak** (0 byte selama 120s idle deadline) —
tingkah laku stokastik free tier, bukan batas keras saiz.

**Masalah lama:** idle pada input besar → percubaan akaun kedua (fingerprint
limit 2) → 244s menunggu + 2 akaun kena cooldown 15m untuk kegagalan yang
hampir pasti (punca provider-wide, bukan akaun).

**Fix (commit `cd55e9c5`):**
- `shouldStopForLargePromptIdle` — prompt ≥200k token yang idle **tanpa
  sebarang bukti generation** berhenti selepas percubaan pertama (~127s,
  bukan 244s). Guard penting: stream yang dah hasilkan bukti apa-apa
  (reasoning/text/tool) TIDAK terjejas — ujian live request 447k token
  berjaya sepenuhnya selepas fix ini.
- Mesej error berpandu: `... (input ~Nk token melebihi kemampuan upstream
  semasa — kecilkan sesi/compact dan cuba semula)` — client/agent tahu kena
  compact, bukan cuba lagi buta.
- Audit jujur: baris gagal idle kini rekod anggaran prompt token (dulu
  in=0 menyembunyikan saiz sebenar).

**Config terlibat:** tiada field baru; threshold tetap 200k token dalam
kod. Guna compaction OpenCode (80k) — jauh di bawah sempadan.

**Peta sempadan context (disahkan 26 Ogos 2026):**

| Saiz prompt | Tingkah laku |
|---|---|
| ≤200k | Stabil terbukti (termasuk 159k+xhigh, 198k+tools) |
| 200k–500k | **Stokastik** — kadang penuh, kadang idle; gagal anggun dalam ~127s |
| >500k | Fail-fast HTTP 400 `context_length_exceeded` (0.2s) |

### Keepalive semasa quality hold (patch #15, 25 Ogos — dibina semula 26 Ogos sebagai v2)

**Gejala:** dalam OpenCode, jawapan muncul dua kali dengan kandungan hampir
serupa (seolah model mengulang). Audit menunjukkan pasangan request dengan
`input_tokens` sama persis beberapa saat antara satu sama lain — OpenCode
menghantar semula request yang sama kerana ia menganggap yang pertama gagal.

**Punca:** semasa quality hold, gateway menahan semua bait sambil menunggu
bukti thinking (fasa senyap 12–135s). Bagi client dengan idle timeout pendek
(OpenCode), senyap sebegini kelihatan seperti sambungan mati → client abort
dan retry → gateway memproses kedua-dua request → jawapan berganda.

**Versi pertama (25 Ogos) — terbukti TIDAK berfungsi.** Suntikan ke dalam
pump (`: grok2api-keepalive` masuk buffer `held`) tidak sampai ke client
semasa senyap kerana (a) response HTTP hanya bermula SELEPAS peek tamat —
sepanjang hold client belum terima headers pun; (b) untuk protokol Chat,
marker itu juga tersenarai dalam `internalSSEMarkerFilter` dan ditapis.
Eksperimen socket mentah: headers hanya tiba pada 67.45s (selepas fasa
thinking), 0 keepalive sampai ke client. Bukti "tiada duplikat selepas
patch" dalam versi awal nota ini datang dari sample kecil/kesan patch #13,
bukan dari mekanisme keepalive.

**Fix v2 (26 Ogos) — keepalive betul-betul ke client:**
- `Input.HoldKeepaliveSink` (gateway): sink yang menerima komen keepalive
  semasa hold. `startHoldKeepalive` menulis TERUS ke sink client pada selang
  `requestRetry.holdKeepalive` — tidak menyentuh pump/buffer `held`, jadi
  replay kekal byte-identical dengan upstream dan scanner tak terganggu.
  Stop function menunggu goroutine keluar (tiada race dengan body copy).
- `streamPreamble` (handler): commit head SSE 200 awal pada tick keepalive
  pertama (`Content-Type: text/event-stream`, `Cache-Control: no-cache`,
  trailer hosted-tool bila diisytiharkan). Idempotent — `WriteHeader` sekali.
- Marker keepalive DIBUANG dari `internalSSEMarkers` — ia kini lulus ke
  client sebagai komen SSE (semua parser SSE abaikannya; kandungan mesej
  tidak terjejas).
- Laluan error selepas head committed: `writeCommittedStreamError` menghantar
  event error in-stream (`data: {"error":...}` + `[DONE]`) memakai code
  sebenar dari `UpstreamFailure` (`streamAbortTrailer` kini map
  `UpstreamFailure.Code`), bukan lagi `upstream_stream_interrupted` generik.

**Bukti live (eksperimen socket mentah, xhigh soalan kompleks):**
sebelum v2 — headers 67.45s, 0 keepalive; selepas v2 — headers 5.92s,
**20 keepalive setiap ~5s sepanjang fasa senyap**, max silence gap 5.0s,
stream penuh 175s/854KB kekal sempurna. Client dengan idle timeout ≥10s
tidak lagi abort & retry.

**Test:** `TestPeekQualityStreamHoldKeepaliveInjectsComments` (komen sampai
ke sink SEMASA senyap + replay bebas kontaminasi),
`TestPeekQualityStreamHoldKeepaliveDisabledByDefault` (0 = tiada suntikan),
`TestPeekQualityStreamHoldKeepaliveStopsWhenSinkFails` (sink gagal → berhenti
tanpa rosakkan peek), `TestStreamPreambleKeepaliveCommitsHeadAndWritesComments`,
`TestStreamPreambleAnnouncesHostedToolTrailer`,
`TestWriteCommittedStreamErrorEmitsInStreamError`,
`TestWriteProtocolResultContinuesAfterPreamble` — lulus `-count=2`, full
suite 63 pakej 0 gagal.

**Image:** `grok2api:local-keepalive-v2` (backup `.env` pra-v2 dalam
`../backups/.env.pre-keepalive-v2.bak`; image lama `local-keepalive` kekal
sebagai fallback).

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

Baseline sihat: **63 pakej lulus, 0 gagal**.

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
| 13 | Early-release stream ber-tools selepas hold deadline | `gateway/quality_retry_scan.go`, `gateway/quality_retry.go` | Jawapan prose ditampal sekali harung pada hujung (splat) selepas 75s senyap — tiada streaming langsung |
| 14 | TTFT audit jujur untuk stream ber-hold (`firstEvidenceAt`/`markAt`) | `gateway/quality_retry_scan.go`, `gateway/timing.go` | `first_token_ms` ≈ duration penuh; TPS terbit jadi ~282k token/s — sampah statistik |
| 15 | Hold keepalive v2 (early SSE head + keepalive sink ke client) | `gateway/quality_retry_scan.go`, `inference/handler.go` | Client idle-timeout pendek abort & retry → jawapan berganda (OpenCode) |
| 16 | Silent-thinking retry (fikir tapi tiada jawapan) | `gateway/quality_retry.go`, `gateway/quality_retry_scan.go` | Stream tamat dengan reasoning sebenar tapi content ~1 token — UI nampak "terputus" |
| 17 | Soft thinking-score ordering per-akaun | `gateway/selector.go` | Akaun benak dipilih separuh masa — tiada ordering jangka panjang |
| 18 | Persist thinking-score seeding (`SeedThinkingScores`) | `gateway/selector.go` | Score hilang bila container restart — akaun benak "belajar semula" dari kosong |
| 19 | Preemptive benak avoidance (`benakAvoid`) + adaptive hold 5s | `gateway/selector_plan.go`, `gateway/service.go` | 44.5% request ialah retry dalaman quality_degraded; 1.7 jam/masa terbuang |
| 20 | Early header abort — instrument-first (default 0s = log sahaja) | `gateway/quality_retry.go`, `gateway/service.go`, `infra/config/config.go`, `app/application.go`, `gateway/quality_header_budget_test.go` | Tiada data masa header utk validasi signal sihat-vs-benak; benak hanya tertangkap selepas holdTimeout (10s) — arm 5s selepas data sah |

**Nota:** Persona AKIF settings hidup dalam `config.yaml` (gitignored — **tak ikut git**). Backup ada kat `../backups/config.yaml.persona_*.bak`.

### Liputan ujian setiap patch (audit 22 Ogos; dilanjutkan 27 Ogos)

Semua patch kini ada ujian. Jalankan `go test ./...` selepas merge — **63 pakej, 0 gagal** = sihat.

| Patch | Fail ujian |
|---|---|
| 1. Reasoning `detailed` | `cli/normalize_test.go` (dah ada sebelum ni) |
| 2. Doom-loop | `conversation/stream_doomloop_test.go` |
| 3. Soft-session v4 | `gateway/soft_session_test.go` |
| 4+5. max_output 65536 | `inference/max_output_tokens_test.go`, `cli/max_output_tokens_test.go` |
| 6+7. Persona (+ berlapis) | `cli/persona_inject_test.go`, `config/persona_test.go` |
| 8. reasoning_opaque replay | `conversation/reasoning_replay_test.go` |
| 10. Hosted-tool diagnostic | `inference/hosted_tools_test.go` (12 ujian) |
| 13-16. Quality hold/retry chain | `gateway/quality_retry_test.go` (termasuk keepalive, silent-thinking) |
| 17-18. Thinking score + seeding | `gateway/selector_test.go` (`TestNoteThinkingAdjustsScore`, `TestSeedThinkingScores`) |
| 19. Benak avoidance | `gateway/selector_layered_test.go` (`TestBenakAvoidSoftQuarantine`) |
| 20. Early header abort | `gateway/quality_header_budget_test.go` (3 ujian) |

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

### Release awal stream ber-tools selepas hold deadline (patch #13, 23 Ogos)

**Gejala:** dengan `qualityGuard.requestRetry` aktif + request yang declare tools
(semua request OpenCode), jawapan prose ditampal sekali harung pada hujung — tiada
streaming langsung, walaupun `holdTimeout` pendek (10s). Ujian: 2701 content event
tiba dalam 0.7s selepas tunggu 74.8s.

**Punca (bug, bukan trade-off):** dalam `peekQualityStream` (`quality_retry_scan.go`),
branch keep-waiting degradation (`DeclaredClientTools > 0 && !SawToolCall && !Terminal`)
tiada jalan keluar bila hold timer dah mati — timer hanya fire sekali, dan bukti
thinking (`: grok2api-reasoning-evidence`) bagi grok-4.6 biasanya sampai **selepas**
deadline (fasa thinking senyap 12–75s) → loop terus tunggu EOF → splat.

**Fix:**
- `holdExpired` latch: deadline direkod persisten selepas timer fire
- Release path baharu: `HasThinking && holdExpired && VisibleRunes >= toolNarrationWindow (160)`
  → Deliver segera, tiada penalti akaun. Guard rune wajib: setiap narasi degradation
  bermula pada offset 0, jadi window bersih ≥160 rune membuktikan stream bukan
  narasi — guard inilah yang halang release menewaskan detector degradation.
- Degradation check kekal LAGI dulu dalam loop setiap iterasi — narasi degraded
  tidak boleh deliver walaupun lewat (ada test khas).

**Kesan:** benak auto-retry + tool-degradation retry/salvage + fail_closed semua
kekal aktif, sambil teks stream live. Semua perlindungan = mod B asal; kelajuan =
mod A. Ujian selepas patch (grok-4.6 ber-tools): nisbah 0.60 (dulu 1.00), sebaran
teks 9.5s berterusan; esei panjang: 30.9s berterusan (dulu 0.7s splat); xhigh+tools:
nisbah 0.58, sebaran 18.0s.

**Test:** 3 unit test baharu dalam `quality_retry_test.go`
(`TestPeekQualityStreamLateThinkingEvidenceReleasesToolStream`,
`TestPeekQualityStreamExpiredHoldWithoutThinkingStillWaits`,
`TestPeekQualityStreamLateEvidenceDoesNotReleaseDegradedNarration`) —
lulus `-count=2`, full suite 62 pakej 0 gagal.

**Image:** `grok2api:local-earlythink` (= local-salvage + patch #13).
Config: `requestRetry.enabled: true`, `holdTimeout: 10s` — teks live dikeluarkan
bukan oleh timeout ringkas tapi oleh bukti thinking lewat; `holdTimeout` kekal
sebagai deadline rotate untuk benak.

### TTFT audit jujur untuk stream ber-hold (patch #14, 24 Ogos)

**Gejala:** audit `first_token_ms` untuk stream yang melalui quality hold ≈
`duration_ms` (contoh sebenar: request esei 74.7s mencatat TTFT **74702ms** —
1ms sebelum tamat). TPS terbit jadi `output / 0.021s ≈ 282k token/s` — sampah;
panel hanya selamat kerana degrade-guard `GenerationWindowMS` yang fallback ke
duration penuh apabila tinggal <1000ms.

**Punca:** first-token di-stamp oleh `responseInspector` semasa delta pertama
**di-forward ke client** (`markFirstTokenForwarded`, handler.go). Stream yang
di-hold sampai EOF kemudian di-replay sekaligus men-forward semua byte pada
hujung → stamp jatuh pada masa replay, bukan masa model mula menjana.

**Nota sampingan:** baris audit dengan `first_token_ms = NULL` +
`error_code='quality_degraded'` + status 200 **bukan bug** — ia baris audit
per-attempt untuk percubaan yang di-withhold sebelum retry
(`recordQualityDegraded` tidak menetapkan first_token). By design.

**Fix (punca, bukan penampang):**
- `qualityScanState.firstEvidenceAt`: scanner peek merakam masa bukti
  generation pertama dari upstream — marker reasoning-start, delta thinking,
  teks kelihatan, atau tool call — semasa stream masih di-hold
  (`noteFirstEvidence`, dipanggil via defer dari `ObserveQualityChunk`).
- `peekQualityStream` memulangkan timestamp itu; laluan deliver men-stamp
  `firstToken.markAt(t)` (kaedah baharu dalam `timing.go`, `sync.Once` —
  forward-mark kemudian jadi no-op) sebelum `handoffResponse`. Laluan retry /
  withhold TIDAK men-stamp (stream dibuang); laluan fail-open fallback membawa
  timestamp attempt masing-masing dalam `qualityFallback.firstEvidenceAt`.
- Laluan tanpa hold tidak berubah (inspector forward-stamp ≈ masa upstream
  memang, sebab tiada buffer).

**Kesan:** TTFT kini = masa model mula menjana (latensi upstream sebenar),
bebas daripada dasar hold. `GenerationWindowMS`/`OutputTokensPerSecond`
mengira window sebenar; degrade-guard <1000ms kekal sebagai pelindung baris
lama.

**Bukti live (request esei 52.4s, client nampak teks mula 25.0s):**
audit `first_token_ms = 24738` ≈ masa bukti upstream — sebelum patch:
74702/74723. Request tools ringkas (1.6s, client 1.6s): `first_token_ms = 818`.

**Test:** `TestFirstTokenTimerMarkAtStampsObservedTime` (timing_test.go),
`TestPeekQualityStreamFirstEvidenceAtIsUpstreamTime`,
`TestPeekQualityStreamFirstEvidenceAtZeroWithoutEvidence`
(quality_retry_test.go) — lulus `-count=2`, full suite 62 pakej 0 gagal.

**Image:** `grok2api:local-ttft` (= local-earlythink + patch #14).

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
