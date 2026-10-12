# Shop documents over iMessage (stock books, distributor invoices, delivery notes)

A trader texts Miriam a photo of their own stock book, or a distributor's invoice
or delivery note. Go recognises the page and hands it to the Python reader, which
owns vision, model routing and the arithmetic checks. The reader's reply is sent
back **verbatim**: every figure in it was checked against the page in plain code,
so nothing downstream may restate one.

## The path

1. **The bridge** carries the attachment (photos and PDFs up to 12MB; voice notes
   keep 4MB). Over the cap it posts an oversized notice instead of the bytes.
2. **Go recognises the page.**
   - *A photo*: the vision pass Go already runs for receipts also reports the
     page's kind. `DocStockBook` / `DocDeliveryNote` take the shop path; receipts,
     invoices and statements keep the path they had.
   - *A PDF*: `document.ClassifyPDF` reads the file's own text layer
     (`document.PDFText` — no rendering, no OCR, bounded, never errors). An
     invoice or delivery note takes the shop path; a PDF that classifies as a
     statement keeps the statement pipeline exactly as before.
   - *A PDF whose text layer says nothing* — a scan, or an exporter this reader
     cannot follow — is handed to the reader as an unknown page, the same way a
     photo no vision pass could classify is. The reader can rasterise a page and
     see it, so it gets the first look. If it declines, times out, errors or
     panics, `shopCannotRead` hands the file to the statement path, which answers
     byte for byte what that PDF got before the reader existed. An unclassifiable
     PDF is therefore never worse off than it was, and a distributor invoice sent
     as a PDF is read instead of declined as a statement.
3. **Go claims the message id** (`platform:shopread:<platform>:<msg_id>` via Redis
   `SetNX`, 15 min; the page's own sha256 when the bridge sent no id) so a
   redelivery cannot read — and charge for — the same page twice.
4. **A holding bubble goes out immediately**, then the read runs in a goroutine on
   a fresh `context.Background()` with a 150s deadline. The inbound POST is
   already answered, so the bridge's own timeout never applies to a read.
5. **The answer lands in the same thread.** `flags: ["failed"]`, an empty reply, a
   timeout, an error or a panic all become one retake line.

## Fail-closed rules

- No reader, no linked owner, an oversized page, a claim-store error, a timeout, a
  reader failure or a panic → one retake line, nothing stored, no figures.
- A shop page never reaches the guest brain: `handleShopDocument` returns before
  the orchestrator, so no model can rephrase a number.
- The reader's reply is what the owner sees. Go adds no figure of its own.
- The owner's caption is passed through. An unlinked sender never reaches the
  reader (there is no account to store the document against) and is onboarded
  as before.

## Settings

- `SHOP_READ_TIMEOUT_S` (default 150) bounds one read. Reads use their own HTTP
  client: the chat client's 60s timeout is far too short for a vision call.
- The reader needs `PYTHON_AGENT_URL`, `JWT_SECRET` (the same short-lived
  per-user token the chat path mints) and Redis for the claim store. Without
  Redis an in-memory store covers a single replica only — the wiring logs that,
  loudly, at startup.

## Running it locally

    # 1. the reader, with a process-local store so no Postgres is needed
    cd MIRIAM
    LLMAPI_API_KEY=... JWT_SECRET=<32+ chars> .venv/bin/python eval/shop_live_server.py

    # 2. the real Go client against it
    cd RAIL_BACKEND
    PYTHON_AGENT_URL=http://127.0.0.1:8099 JWT_SECRET=<the same secret> \
      SHOP_READ_USER_ID=<rail user uuid> \
      go run ./scripts/shop_read_smoke \
        -file ../MIRIAM/tests/fixtures/stock_book_mama_tobi.png -hint stock_book

## Read live

Every row below is the real Go client (`scripts/shop_read_smoke`) against the real
reader on llmapi.ai, with the reply the owner would have seen. Latency is the
whole round trip the smoke client measures; cost is the same page and the same
tier measured by the eval harness (`eval/shop_read_eval.py --modes big`), which
reports the provider's own token cost per read.

| page | form | hint | latency | cost/read | what came back |
| --- | --- | --- | --- | --- | --- |
| Mama Tobi stock book | PNG | stock_book | 28.8s | $0.0138 | 7 items, computed ₦286,300 against the written ₦254,400, gap ₦31,900; one row does not add up (Dangote Spag); low stock Indomie 7 and Milo 400g 7; crossed-out 8 to 9; two notes |
| Ledger v2 | PNG | stock_book | 12.0s | $0.0134 | 7 items, computed ₦248,000 against the written ₦236,000, gap ₦12,000; every row adds up; low stock Milo 400g 6 |
| Ledger v2 | scanned PDF, no text layer | stock_book | 14.4s | $0.0184 | the same reply, figure for figure: the page is rasterised and read as an image |

The scanned PDF costs more than its PNG because rasterising a full page spends
more image tokens than the smaller photo does. Two pages and three forms is what
has been read live; the page itself is a fixture, not a real trader's book.

## Full stack without Docker

The whole chain runs natively: no containers, no AMQP. Inbound is processed
in-request (`HandleInbound` calls `processor.Process` directly; the platform
package contains no AMQP code), so the broker is only needed by deployments that
consume from a queue instead of HTTP.

    # 1. Postgres + Redis natively, then the reader's own schema
    createdb miriam_local
    DATABASE_URL=postgresql+asyncpg://user@127.0.0.1:5432/miriam_local \
      .venv/bin/python -m alembic upgrade head          # 003 = shop tables
    DATABASE_URL=postgresql+asyncpg://user@127.0.0.1:5432/miriam_local \
      JWT_SECRET=<32+ chars> LLMAPI_API_KEY=... ENVIRONMENT=development \
      .venv/bin/uvicorn miriam_agent.api.main:app --port 8099

    # 2. The Go backend, against the same Postgres
    DATABASE_URL=postgres://user@127.0.0.1:5432/rail_sim?sslmode=disable \
      REDIS_HOST=127.0.0.1 JWT_SECRET=<the same secret> \
      ENVIRONMENT=development EMAIL_PROVIDER=log EMAIL_ENVIRONMENT=development \
      PLATFORM_ENABLED=true PLATFORM_BRIDGE_HMAC_SECRET=<shared> \
      PLATFORM_BRIDGE_BASE_URL=http://127.0.0.1:3100 \
      PYTHON_AGENT_ENABLED=true PYTHON_AGENT_URL=http://127.0.0.1:8099 \
      go run ./cmd

Two things bite on the first run: `configs/config.yaml` ships
`environment: production`, and production validation demands keys a laptop does
not have (so set `ENVIRONMENT=development`), and the email service needs
`EMAIL_PROVIDER=log` outside production. The API listens on 8081, not 8080.

    # 3. one page through the chain (harness in scripts/e2e/)
    BRIDGE_HMAC_SECRET=<shared> E2E_DATABASE_URL=<the Go Postgres> \
      python3 scripts/e2e/shop_inbound_local.py \
        ../MIRIAM/tests/fixtures/wigmore_invoice_WGT-2610-0417.pdf

What has been proven this way, with the reader on Postgres: a distributor PDF
(Go classifies its own text layer, no OCR needed) goes bridge-shaped inbound →
processor → holding bubble at 0.0s → reader on the big tier → checked reply at
34.1s → `BridgeDispatcher.SendChatMessage` → the bridge's `/send`. Two uploads of
the same page store **one** `shop_documents` row (`find_by_sha`), and the row,
its five invoice lines, its trust label and its model path are all readable in
Postgres afterwards. A bare *photo* with no caption and no OCR pass at all
(`E2E_CAPTION=""` in the same harness) reaches the reader as `unknown_image`,
comes back as `stock_book` with `page classified by the reader: stock_book` in
its stored flags at 42.2s, and when the reader instead declines the page the
owner gets that photo's own pre-reader line, once more byte for byte.

## Known gaps

- A photo Go cannot classify is classified by the reader instead: Go hands the
  page over as `unknown_image`, the reader spends one cheap call naming it, and
  only the reader's own kinds are read. A page nobody can name — a receipt, a
  landscape, a blank shot — is declined and answered with the exact line that
  photo got before the reader existed. So a photo of a handwritten book is read
  on a machine with no OCR at all, at the price of that one extra call; with OCR
  the vision pass still classifies first, and the call is skipped. PDFs remain
  the dependable case.
- The bridge does not forward a caption typed with an attachment (`text: ""`), so
  a photo arrives at the reader without the owner's words.
- Document attachments are still capped at 4 MiB (`maxStatementAttachmentBytes`)
  before the shop classification runs, so a 4 to 12 MiB distributor PDF is
  answered as too large even though the bridge now carries 12 MiB. Raising it
  changes what the statement path answers for those files, which is why it was
  left alone.
- Three forms of two fixture pages have been read live (see above). There is
  still no golden set of real traders' stock books, which is where a handwriting
  model has to earn its keep.
