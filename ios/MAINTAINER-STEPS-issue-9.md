# Issue #9 Phase 1 — maintainer steps

One-time Xcode UI work for issue #9 Phase 1. Delete this file after merge.

## Why these steps aren't automated

Xcode's `project.pbxproj` is brittle enough that scripted edits sometimes
produce a project that opens but has subtle UI weirdness. The agent prepared
all Swift sources, the xcconfig, and `Spur-Info.plist`; the SPM dep + a
couple of project setting flips need a human at the keyboard.

Total time: ~3 minutes.

## Prereqs

- Local Supabase running: `supabase start` (from repo root). Note: the
  `[auth.sms.test_otp]` key changed to `14152127777` (E.164 digits-only)
  in this branch; if you had Supabase running from before this branch,
  `supabase stop` and `supabase start` to pick up the config change.
- Go server running: `cd server && make run` (after `cp .env.example .env`
  and pasting in `supabase status` values)

## Step 1 — Add `supabase-swift` via SPM

1. Open `ios/Spur/Spur.xcodeproj` in Xcode.
2. **File → Add Package Dependencies…**
3. Search bar (top right): paste `https://github.com/supabase/supabase-swift`
4. Dependency Rule: **Up to Next Major Version**, leave the auto-filled
   minimum alone (latest 2.x at time of writing).
5. Click **Add Package**.
6. In the product picker, check **`Supabase`** (the umbrella library) and
   confirm the target is **Spur** (not the test targets — they don't need it).
   Click **Add Package**.

Wait for SPM to resolve. The package navigator will show `supabase-swift`
with a few siblings (Auth, PostgREST, Realtime, Storage, Functions, Helpers).

## Step 2 — Attach `Local.xcconfig` to the project

1. Click the **Spur** project in the navigator (the blue icon at the top,
   not the yellow folder).
2. With **Project** (not Target) selected in the second column → **Info**
   tab → **Configurations** section.
3. For **Debug**: expand the row, click the dropdown next to **Spur**, pick
   **Local**.
4. For **Release**: same — pick **Local** (until a Staging xcconfig
   exists, both configs use Local).

## Step 3 — Confirm Info.plist wiring

1. Select the **Spur** target → **Build Settings** tab.
2. Search box: type `Info.plist File`.
3. The value should resolve to `Spur-Info.plist` (inherited from the
   xcconfig — shown in light grey if you haven't overridden it).
4. If it's blank or shows something else, the xcconfig didn't attach;
   redo Step 2.

## Step 4 — Build

1. **Product → Clean Build Folder** (⇧⌘K) — clears any stale state from
   pre-supabase-swift.
2. **Product → Build** (⌘B). Should compile clean. The earlier
   "Cannot find 'AuthModel' in scope" cascade was downstream of the missing
   Supabase module; it clears once the SPM dep is in.

## Step 5 — End-to-end verify

1. Pick an **iPhone 17 Pro** (or any iOS 26.x) simulator from the run
   destination menu.
2. **⌘R**.
3. Sign-in screen appears.
4. Country selector: leave at **🇺🇸 +1** (default). Phone field: `4152127777`
   → **Send code**. The client formats E.164 (`+14152127777`) before sending.
5. Code field: `123456` → **Verify**
6. Map appears.
7. Tap the person-icon button (top-right) → **Ping /me** → alert with
   `OK — user_id: <UUID>`. That UUID is the same one Supabase issued for
   this phone-number's auth row.
8. **Force-quit** the app from the simulator (Cmd+Shift+H twice → swipe up).
9. Re-launch. Should go straight to the map — session was persisted to
   the keychain. No re-sign-in.
10. Tap person-icon → **Sign out** → back to sign-in screen.

## Step 6 — Confirm the public.users row

In another terminal:

```bash
psql postgresql://postgres:postgres@127.0.0.1:54322/postgres \
  -c "SELECT id, created_at FROM public.users ORDER BY created_at DESC LIMIT 1;"
```

The most recent row's `id` should match the UUID from the `/me` alert.
That confirms migration 0006's trigger fired during the Supabase Auth signup
transaction (per ADR 0022).

## After verification

- Tell the agent the verify passed; it'll open the PR for issue #9 Phase 1.
- Delete this file as part of the PR (or in a follow-up commit).
