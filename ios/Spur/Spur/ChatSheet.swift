import SwiftUI

// Per-Event chat sheet (#65). Opened from EventDetailSheet for Committed
// Attendees on unlocked Events. SSE stream is held open while the sheet is
// visible; cancelled on dismiss via Task cancellation (also closes the
// server-side stream via request-context cancellation).
struct ChatSheet: View {
  let event: NearbyEvent

  @Environment(AuthModel.self) private var auth
  @Environment(\.dismiss) private var dismiss

  @State private var messages: [ChatAPI.Message] = []
  @State private var draft: String = ""
  @State private var loadError: String?
  @State private var sendError: String?
  @State private var sending = false
  @State private var streamTask: Task<Void, Never>?

  var body: some View {
    NavigationStack {
      VStack(spacing: 0) {
        if let loadError {
          Text(loadError)
            .font(.footnote)
            .foregroundStyle(.red)
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
        }
        messageList
        Divider()
        composer
      }
      .navigationTitle(event.title)
      .navigationBarTitleDisplayMode(.inline)
      .toolbar {
        ToolbarItem(placement: .cancellationAction) {
          Button("Close") { dismiss() }
        }
      }
    }
    .presentationDetents([.large])
    .presentationDragIndicator(.visible)
    .task {
      await loadHistory()
      streamTask = Task { await consumeStream() }
    }
    .onDisappear {
      streamTask?.cancel()
      streamTask = nil
    }
  }

  private var messageList: some View {
    ScrollViewReader { proxy in
      ScrollView {
        LazyVStack(alignment: .leading, spacing: 8) {
          ForEach(messages) { m in
            messageRow(m)
              .id(m.id)
          }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
      }
      .onChange(of: messages.last?.id) { _, newID in
        guard let newID else { return }
        withAnimation { proxy.scrollTo(newID, anchor: .bottom) }
      }
    }
  }

  // System messages render centred and quieter; user messages render as
  // standard chat bubbles. v0 doesn't distinguish "from me" vs "from them"
  // visually — Display-name is omitted entirely (the project doesn't expose
  // it on the message payload), so all user messages look the same. A
  // follow-up slice can wire sender display names through the projection.
  @ViewBuilder
  private func messageRow(_ m: ChatAPI.Message) -> some View {
    if m.kind == "system" {
      HStack {
        Spacer()
        Text(m.body)
          .font(.footnote)
          .foregroundStyle(.secondary)
          .padding(.horizontal, 12)
          .padding(.vertical, 6)
          .background(Color.secondary.opacity(0.1), in: Capsule())
        Spacer()
      }
      .accessibilityIdentifier("chat.systemMessage")
    } else {
      Text(m.body)
        .font(.body)
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(Color.accentColor.opacity(0.15), in: RoundedRectangle(cornerRadius: 12))
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityIdentifier("chat.userMessage")
    }
  }

  private var composer: some View {
    VStack(spacing: 4) {
      if let sendError {
        Text(sendError)
          .font(.footnote)
          .foregroundStyle(.red)
          .frame(maxWidth: .infinity, alignment: .leading)
          .padding(.horizontal, 16)
      }
      HStack(spacing: 8) {
        TextField("Message", text: $draft, axis: .vertical)
          .lineLimit(1...4)
          .textFieldStyle(.roundedBorder)
          .accessibilityIdentifier("chat.composer")
        Button(action: { Task { await send() } }) {
          if sending {
            ProgressView().controlSize(.small)
          } else {
            Image(systemName: "arrow.up.circle.fill")
              .font(.title2)
          }
        }
        .disabled(sending || trimmed.isEmpty)
        .accessibilityIdentifier("chat.sendButton")
      }
      .padding(.horizontal, 16)
      .padding(.vertical, 8)
    }
  }

  private var trimmed: String {
    draft.trimmingCharacters(in: .whitespacesAndNewlines)
  }

  private func loadHistory() async {
    loadError = nil
    do {
      let history = try await ChatAPI.fetchHistory(eventID: event.id, auth: auth)
      messages = history
    } catch {
      loadError = error.localizedDescription
    }
  }

  // consumeStream runs for the lifetime of the sheet. AsyncThrowingStream
  // iteration ends on task cancellation (sheet dismiss) or transport
  // error; in the latter case we surface the error to the user and stop —
  // tapping reopen retries the whole connection.
  private func consumeStream() async {
    let last = messages.last?.id
    let stream = ChatAPI.openStream(eventID: event.id, lastEventID: last, auth: auth)
    do {
      for try await msg in stream {
        // De-dupe against optimistic local insert + replay overlap with
        // history. Monotonic id makes this a single contains-check.
        if !messages.contains(where: { $0.id == msg.id }) {
          messages.append(msg)
        }
      }
    } catch {
      loadError = "Live updates dropped: \(error.localizedDescription)"
    }
  }

  private func send() async {
    let body = trimmed
    guard !body.isEmpty else { return }
    sending = true
    sendError = nil
    defer { sending = false }
    do {
      let sent = try await ChatAPI.sendMessage(eventID: event.id, body: body, auth: auth)
      // Optimistic-light: append immediately so the user sees their
      // message; the SSE stream will also deliver this id but the
      // contains-check in consumeStream de-dupes.
      if !messages.contains(where: { $0.id == sent.id }) {
        messages.append(sent)
      }
      draft = ""
    } catch let err as ChatAPI.APIError {
      sendError = err.errorDescription
    } catch {
      sendError = error.localizedDescription
    }
  }
}
