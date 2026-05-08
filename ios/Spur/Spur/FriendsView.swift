import SwiftUI

struct FriendsView: View {
  @Environment(AuthModel.self) private var auth

  @State private var friends: [FriendsAPI.Friend] = []
  @State private var incoming: [FriendsAPI.Request] = []
  @State private var outgoing: [FriendsAPI.Request] = []
  @State private var loadError: String?
  @State private var isLoading = false
  @State private var hasLoaded = false

  @State private var searchText: String = ""
  @State private var searchResults: [FriendsAPI.Candidate] = []
  @State private var searchTask: Task<Void, Never>?

  @State private var unfriendTarget: FriendsAPI.Friend?
  @State private var actionError: String?

  var body: some View {
    NavigationStack {
      Group {
        if isSearching {
          searchList
        } else {
          mainList
        }
      }
      .navigationTitle("Friends")
      .searchable(
        text: $searchText, placement: .navigationBarDrawer(displayMode: .always),
        prompt: "Find by display name"
      )
      .onChange(of: searchText) { _, newValue in
        scheduleSearch(query: newValue)
      }
      .refreshable { await reload() }
      .task {
        if !hasLoaded { await reload() }
      }
      .alert(
        "Unfriend?",
        isPresented: Binding(
          get: { unfriendTarget != nil },
          set: { if !$0 { unfriendTarget = nil } }
        ),
        presenting: unfriendTarget
      ) { friend in
        Button("Unfriend", role: .destructive) {
          Task { await performUnfriend(friend) }
        }
        Button("Cancel", role: .cancel) {}
      } message: { friend in
        let name = friend.displayName.isEmpty ? "this user" : friend.displayName
        Text("Remove \(name) from your friends?")
      }
      .alert(
        "Couldn't complete that",
        isPresented: Binding(
          get: { actionError != nil },
          set: { if !$0 { actionError = nil } }
        ),
        presenting: actionError
      ) { _ in
        Button("OK") { actionError = nil }
      } message: { msg in
        Text(msg)
      }
    }
  }

  private var isSearching: Bool {
    !searchText.trimmingCharacters(in: .whitespaces).isEmpty
  }

  // MARK: main list (Requests + Friends sections)

  @ViewBuilder
  private var mainList: some View {
    if let err = loadError, !hasLoaded {
      ContentUnavailableView(
        "Couldn't load friends",
        systemImage: "exclamationmark.triangle",
        description: Text(err))
    } else if hasLoaded && friends.isEmpty && incoming.isEmpty && outgoing.isEmpty {
      ContentUnavailableView(
        "No friends yet",
        systemImage: "person.2",
        description: Text("Search by display name to send a request."))
    } else {
      List {
        if !incoming.isEmpty || !outgoing.isEmpty {
          Section("Requests") {
            ForEach(incoming) { req in
              IncomingRequestRow(
                request: req,
                onAccept: { await performAccept(req) },
                onReject: { await performReject(req) })
            }
            ForEach(outgoing) { req in
              OutgoingRequestRow(
                request: req,
                onWithdraw: { await performWithdraw(req) })
            }
          }
        }
        if !friends.isEmpty {
          Section("Friends") {
            ForEach(friends) { friend in
              FriendRow(friend: friend)
                .swipeActions(edge: .trailing) {
                  Button(role: .destructive) {
                    unfriendTarget = friend
                  } label: {
                    Label("Unfriend", systemImage: "person.badge.minus")
                  }
                }
            }
          }
        }
      }
      .listStyle(.insetGrouped)
    }
  }

  // MARK: search results

  @ViewBuilder
  private var searchList: some View {
    if searchResults.isEmpty {
      ContentUnavailableView.search(text: searchText)
    } else {
      List(searchResults) { candidate in
        HStack {
          AvatarBubble(name: candidate.displayName)
          Text(candidate.displayName)
            .font(.body)
          Spacer()
          Button {
            Task { await performSend(candidate) }
          } label: {
            Text("Send request")
              .font(.subheadline.weight(.medium))
          }
          .buttonStyle(.borderedProminent)
          .controlSize(.small)
          .tint(.accentColor)
        }
      }
      .listStyle(.insetGrouped)
    }
  }

  // MARK: data

  private func reload() async {
    isLoading = true
    defer { isLoading = false }
    do {
      async let friendsTask = FriendsAPI.listFriends(auth: auth)
      async let requestsTask = FriendsAPI.listRequests(auth: auth)
      let (f, r) = try await (friendsTask, requestsTask)
      friends = f
      incoming = r.incoming
      outgoing = r.outgoing
      loadError = nil
      hasLoaded = true
    } catch {
      loadError = error.localizedDescription
      hasLoaded = true
    }
  }

  private func scheduleSearch(query: String) {
    searchTask?.cancel()
    let trimmed = query.trimmingCharacters(in: .whitespaces)
    guard !trimmed.isEmpty else {
      searchResults = []
      return
    }
    searchTask = Task {
      // Debounce ~250ms so each keystroke doesn't fire a request.
      try? await Task.sleep(nanoseconds: 250_000_000)
      if Task.isCancelled { return }
      do {
        let results = try await FriendsAPI.searchCandidates(query: trimmed, auth: auth)
        if !Task.isCancelled { searchResults = results }
      } catch {
        if !Task.isCancelled { searchResults = [] }
      }
    }
  }

  private func performSend(_ candidate: FriendsAPI.Candidate) async {
    do {
      try await FriendsAPI.sendRequest(recipientID: candidate.userID, auth: auth)
      searchText = ""
      searchResults = []
      await reload()
    } catch {
      actionError = error.localizedDescription
    }
  }

  private func performAccept(_ req: FriendsAPI.Request) async {
    do {
      try await FriendsAPI.acceptRequest(requesterID: req.requester, auth: auth)
      await reload()
    } catch {
      actionError = error.localizedDescription
    }
  }

  private func performReject(_ req: FriendsAPI.Request) async {
    do {
      try await FriendsAPI.rejectRequest(requesterID: req.requester, auth: auth)
      await reload()
    } catch {
      actionError = error.localizedDescription
    }
  }

  private func performWithdraw(_ req: FriendsAPI.Request) async {
    do {
      try await FriendsAPI.withdrawRequest(recipientID: req.recipient, auth: auth)
      await reload()
    } catch {
      actionError = error.localizedDescription
    }
  }

  private func performUnfriend(_ friend: FriendsAPI.Friend) async {
    do {
      try await FriendsAPI.unfriend(friendID: friend.friendID, auth: auth)
      await reload()
    } catch {
      actionError = error.localizedDescription
    }
  }
}

private struct IncomingRequestRow: View {
  let request: FriendsAPI.Request
  let onAccept: () async -> Void
  let onReject: () async -> Void

  var body: some View {
    HStack {
      AvatarBubble(name: request.displayName)
      VStack(alignment: .leading, spacing: 2) {
        Text(displayLabel)
          .font(.body)
        Text("Wants to be friends")
          .font(.caption)
          .foregroundStyle(.secondary)
      }
      Spacer()
      Button {
        Task { await onAccept() }
      } label: {
        Image(systemName: "checkmark")
          .frame(width: 28, height: 28)
      }
      .buttonStyle(.borderedProminent)
      .tint(.green)
      .controlSize(.small)
      .accessibilityLabel("Accept")

      Button(role: .destructive) {
        Task { await onReject() }
      } label: {
        Image(systemName: "xmark")
          .frame(width: 28, height: 28)
      }
      .buttonStyle(.bordered)
      .controlSize(.small)
      .accessibilityLabel("Reject")
    }
    .buttonStyle(.borderless)  // ensure individual buttons get the row's tap, not the row
  }

  private var displayLabel: String {
    request.displayName.isEmpty ? "(no name)" : request.displayName
  }
}

private struct OutgoingRequestRow: View {
  let request: FriendsAPI.Request
  let onWithdraw: () async -> Void

  var body: some View {
    HStack {
      AvatarBubble(name: request.displayName)
      VStack(alignment: .leading, spacing: 2) {
        Text(displayLabel)
          .font(.body)
        Text("Request sent")
          .font(.caption)
          .foregroundStyle(.secondary)
      }
      Spacer()
      Button {
        Task { await onWithdraw() }
      } label: {
        Text("Withdraw")
          .font(.subheadline)
      }
      .buttonStyle(.bordered)
      .controlSize(.small)
    }
    .buttonStyle(.borderless)
  }

  private var displayLabel: String {
    request.displayName.isEmpty ? "(no name)" : request.displayName
  }
}

private struct FriendRow: View {
  let friend: FriendsAPI.Friend

  var body: some View {
    HStack {
      AvatarBubble(name: friend.displayName)
      Text(friend.displayName.isEmpty ? "(no name)" : friend.displayName)
        .font(.body)
      Spacer()
    }
  }
}

private struct AvatarBubble: View {
  let name: String

  var body: some View {
    ZStack {
      Circle()
        .fill(Color.secondary.opacity(0.18))
      Text(initials)
        .font(.subheadline.weight(.semibold))
        .foregroundStyle(.secondary)
    }
    .frame(width: 36, height: 36)
  }

  private var initials: String {
    let parts = name.split(separator: " ").prefix(2)
    let chars = parts.compactMap { $0.first }
    return String(chars).uppercased().isEmpty ? "?" : String(chars).uppercased()
  }
}
