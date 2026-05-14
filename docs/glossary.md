# Glossary

External / technical terms that show up in this repo, in issues, in
ADRs, and in agent conversations. Stays alphabetical.

**Scope**: industry / framework / vendor terminology. Spur's *product*
vocabulary (Commit, Event, Host, Withdraw, etc.) lives in
[`../CONTEXT.md`](../CONTEXT.md). Project-specific decisions live in
[`./adr/`](./adr/). When in doubt, technical-and-external goes here;
domain-and-internal goes in CONTEXT.md.

**Maintenance**: when you introduce or encounter a term a future reader
might not know, add an entry. Two- or three-sentence definitions are
the right length — link out for depth.

---

## A

- **A2P** — Application-to-Person. A business or app sending SMS to a
  consumer, as opposed to P2P (person-to-person) texts. US carriers
  treat A2P traffic as regulated and require registration via
  [10DLC](#1) before delivery.

- **A2P 10DLC** — The US framework for sending business SMS from a
  regular 10-digit phone number. Requires brand + campaign
  registration via The Campaign Registry (TCR). Setup takes 1–7 days
  and has both per-registration and monthly fees. See
  [Twilio's overview](https://www.twilio.com/docs/messaging/compliance/a2p-10dlc).

- **ADR** — Architecture Decision Record. Markdown file in
  [`./adr/`](./adr/) capturing a decision that's hard to reverse,
  surprising to a reader, or has real trade-offs. Spur uses these for
  load-bearing technical posture (e.g.
  [ADR 0005](./adr/0005-supabase-data-plane-go-server-business-logic.md)
  for the Go-server-vs-PL/pgSQL boundary).

- **AFK** — Away From Keyboard. Spur's label for an issue specified
  thoroughly enough that an agent can finish it unsupervised. Contrast
  HITL.

- **Annotation** — A point on a map view that marks a place (e.g.
  the pin for an Event). MapKit and MapLibre Native both call these
  "annotations" — the noun covers both the data (`MLNAnnotation`,
  carrying coordinate + payload) and its on-screen rendering. Spur
  uses one Annotation per Event on the Browse map.

- **Anon key** — Supabase's client-safe API key (also marketed as
  "publishable key"). Different from the service-role key, which
  bypasses RLS and must stay server-side. Per
  [ADR 0014](./adr/0014-secret-management.md), the anon key is
  committable in iOS xcconfig.

- **ATS** — App Transport Security. Apple's iOS policy that blocks
  plaintext HTTP requests by default. Local development against
  `127.0.0.1` requires the
  `NSAppTransportSecurity → NSAllowsLocalNetworking` exception in
  Info.plist.

## C

- **Carrier** — A mobile network operator (Verizon, AT&T, T-Mobile in
  the US). Carriers enforce A2P 10DLC and other SMS-deliverability
  rules.

- **Callout** — The small popup balloon that appears above an
  Annotation when it's tapped on a map (Apple Maps shows a name +
  chevron tile, for example). MapLibre Native shows a built-in
  callout when an annotation has a non-nil `title`; Spur opts out
  via `mapView(_:annotationCanShowCallout:)` and goes straight from
  pin tap to a SwiftUI bottom-sheet.

- **Carrier registration** — The process of submitting a brand
  identity + use-case description ("campaign") to The Campaign
  Registry so US carriers will deliver your A2P SMS. See A2P 10DLC.

- **Cloudflare Worker** — Serverless code that runs at Cloudflare's
  edge, used in Spur to front the PMTiles map archive (see
  [ADR 0021](./adr/0021-self-hosted-pmtiles-cloudflare-worker.md)).

- **CLI** — Command-Line Interface. Tool you run in a terminal.

- **CodingKeys** — Swift convention for mapping JSON wire-format keys
  (snake_case) to Swift property names (lowerCamelCase) in `Decodable`
  types. See `ServerProbe.MeResponse` for an example.

## E

- **E.164** — The international phone-number format standard:
  `+<country><number>`, no spaces or dashes (e.g. `+14152127777`).
  Twilio and Supabase Auth normalize phone input to this shape; the
  iOS country picker constructs E.164 from dial code + digits.

## F

- **Fly / Fly.io** — Hosting platform Spur uses for the Go server (see
  [ADR 0015](./adr/0015-deploy-target-fly-io.md)). Provides a built-in
  secret store via `fly secrets`.

- **FoundationModels** — iOS 26 framework giving on-device access to
  an Apple-provided large language model. Spur uses it for moderation
  per [ADR 0020](./adr/0020-ios-bootstrap.md) so user-generated
  content doesn't go to a third-party LLM provider.

## G

- **GoTrue** — The open-source Go service that powers Supabase Auth.
  Handles signup, sign-in, JWT issuance, JWKS, and SMS provider
  integration.

## H

- **HITL** — Human In The Loop. Spur's label for an issue that needs
  maintainer judgment or external action (e.g. provisioning a
  third-party account). Contrast AFK.

## I

- **Info.plist** — iOS configuration file embedded in every `.app`
  bundle. Contains app metadata, entitlements, and (for Spur) the
  custom `SUPABASE_URL` / `SUPABASE_ANON_KEY` / `SERVER_URL` keys read
  at runtime via `Bundle.main.object(forInfoDictionaryKey:)`.

## J

- **JWKS** — JSON Web Key Set. The set of public keys a JWT issuer
  publishes (Supabase exposes its set at
  `<supabase-url>/auth/v1/.well-known/jwks.json`). The Go middleware
  fetches this set on startup and uses it to verify each incoming
  JWT's signature.

- **JWT** — JSON Web Token. A signed, base64-encoded token containing
  user identity claims (`sub`, `iss`, `exp`, etc.). Spur's Go server
  trusts the `sub` (a UUID) only after JWKS-verifying the signature.

## L

- **Liquid Glass** — Apple's iOS 26 visual design system. Spur's
  baseline UI style per ADR 0020.

## M

- **MapLibre Native** — Open-source vector-tile renderer for iOS
  (UIKit-based, bridged to SwiftUI). Forked from the Mapbox GL Native
  before Mapbox went proprietary. Pinned to 6.26.0 per ADR 0020.

## N

- **NANP** — North American Numbering Plan. Country code `+1`, covers
  US, Canada, and ~20 Caribbean nations. The country picker lists
  them as separate rows with the same dial code.

## O

- **OTP** — One-Time Password. The 6-digit code sent via SMS during
  Spur sign-in. Local development uses Supabase's `[auth.sms.test_otp]`
  short-circuit (no real SMS); staging will use Twilio.

## P

- **pbxproj** — `project.pbxproj`, Xcode's project file. Tracked in
  git per ADR 0020 even though merge conflicts are a known risk; the
  alternatives (xcodegen, Tuist) earn their keep only on multi-dev
  teams.

- **pgx** — Go's most-used PostgreSQL driver, used by Spur's Go server
  to talk to Supabase Postgres.

- **PMTiles** — Single-file map-tile archive format from Protomaps.
  Spur hosts a PMTiles file in Cloudflare R2 and serves XYZ tiles to
  iOS via a Cloudflare Worker (ADR 0021).

- **pre-commit** — A Python framework (https://pre-commit.com) for
  running formatters / linters on staged files before each commit.
  Spur uses it to run gofmt, goimports, golangci-lint, and
  swift-format. Config lives in `.pre-commit-config.yaml`.

- **Protomaps** — Open-source vector-map ecosystem (basemap styles,
  PMTiles, themes). Spur's map style is the unmodified `@protomaps/basemaps`
  light theme.

- **Publishable key** — Supabase's marketing name for the anon key
  (the new `sb_publishable_…` prefix). Client-safe; can ship in
  xcconfig per ADR 0014.

## R

- **R2** — Cloudflare's S3-compatible object storage. Hosts Spur's
  PMTiles archive (ADR 0021).

- **RLS** — Row-Level Security. Postgres feature for declarative
  per-row access policies. Spur uses RLS for Supabase-direct reads
  (the iOS app reads via the anon key + RLS); writes go through the
  Go server.

## S

- **SDK** — Software Development Kit. A library/framework you import
  into your code to talk to a service. Spur's iOS app uses
  `supabase-swift` (the Supabase iOS SDK).

- **Service-role key** — Supabase's server-only API key that bypasses
  RLS. Lives in Fly secrets in hosted, in `server/.env` locally.
  **Never** ships to the iOS client.

- **SMS** — Short Message Service. Standard text message.

- **SPM** — Swift Package Manager. Apple's dependency manager for
  Swift, used to pull in MapLibre and supabase-swift.

- **swift-format** — Apple's official Swift formatter. Run as a
  git pre-commit hook. CI runs it
  in `--strict` mode, which catches naming-convention issues
  (e.g. `user_id` → `userID`) that the format-only mode doesn't.

## T

- **TCR** — The Campaign Registry. The third-party clearinghouse US
  carriers consult to vet A2P 10DLC senders. See
  [www.campaignregistry.com](https://www.campaignregistry.com/).

- **Twilio** — SMS / voice / messaging provider. Spur uses it (or
  will, post-Phase-2) to deliver real OTPs in hosted environments.
  See **Twilio Verify** below for the specific product variant.

- **Twilio Programmable Messaging** — Twilio's general-purpose SMS
  API. You write the message body and Twilio delivers it. For US
  numbers, requires A2P 10DLC. Right tool when you need full message
  control.

- **Twilio Verify** — Twilio's purpose-built OTP product. Twilio
  generates and verifies the code; you call `start` and `check`
  endpoints. No 10DLC registration. Slightly higher per-message cost
  but lower setup overhead. Spur uses this in staging.

## U

- **UUID** — Universally Unique Identifier. 128-bit ID, e.g.
  `f43a3a70-7514-4090-9370-e831d61e5c15`. Spur user IDs are UUIDs
  generated by Supabase Auth.

## V

- **Verify Service SID** — Twilio Verify's identifier for a specific
  Verify Service (one per use case). Pasted into Supabase Auth's
  Twilio Verify config alongside Account SID + Auth Token.

## X

- **xcconfig** — Xcode's plain-text build-configuration file format
  (`.xcconfig`). One file per environment (Local, Staging, eventually
  Prod). Per ADR 0014, Spur reads `SUPABASE_URL` etc. from xcconfig
  → Info.plist → `Bundle.main.object(forInfoDictionaryKey:)`.

- **Xcode synchronized folders** — Xcode 26 feature where files added
  to a folder on disk are auto-included in the build target without
  editing `project.pbxproj`. ADR 0020 §1 takes advantage of this.
