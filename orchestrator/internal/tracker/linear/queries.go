package linear

// GraphQL query bodies. Kept isolated in their own file so the Linear schema
// drift surface lives in one place (per spec §11.2 "Keep query construction
// isolated and test the exact query fields/types required by this
// specification.").
//
// Variable conventions:
//   $projectSlug : ID!  (Linear project slugId)
//   $states      : [String!]!
//   $issueIds    : [ID!]!
//   $first       : Int  (page size, default 50)
//   $after       : String (cursor)
//
// The orchestrator and tests reference these names directly.

// queryCandidateIssues fetches a page of issues from a Linear project whose
// state is in $states. Symphony spec §11.2.
//
// Returns the fields needed to construct a domain.Issue per §4.1.1 and
// §11.3 (lowercased labels, blocked_by from inverse relations where type
// is `blocks`).
const queryCandidateIssues = `
query CandidateIssues($projectSlug: String!, $states: [String!]!, $first: Int!, $after: String) {
  issues(
    filter: {
      project: { slugId: { eq: $projectSlug } }
      state: { name: { in: $states } }
    }
    first: $first
    after: $after
    orderBy: createdAt
  ) {
    pageInfo {
      hasNextPage
      endCursor
    }
    nodes {
      id
      identifier
      title
      description
      priority
      branchName
      url
      createdAt
      updatedAt
      assignee { id name email }
      state { name }
      labels { nodes { name } }
      inverseRelations {
        nodes {
          type
          issue { id identifier state { name } createdAt updatedAt }
        }
      }
    }
  }
}
`

// queryIssueStatesByIDs fetches the current state for each of $issueIds.
// Symphony spec §11.1 / §8.5 (reconciliation).
const queryIssueStatesByIDs = `
query IssueStatesByIDs($issueIds: [ID!]!) {
  issues(filter: { id: { in: $issueIds } }, first: 250) {
    nodes {
      id
      identifier
      state { name }
    }
  }
}
`

// queryTerminalIssues fetches issues in $states (used at startup to remove
// stale workspaces from terminal-state issues). Symphony spec §8.6.
const queryTerminalIssues = `
query TerminalIssues($projectSlug: String!, $states: [String!]!, $first: Int!, $after: String) {
  issues(
    filter: {
      project: { slugId: { eq: $projectSlug } }
      state: { name: { in: $states } }
    }
    first: $first
    after: $after
  ) {
    pageInfo {
      hasNextPage
      endCursor
    }
    nodes {
      id
      identifier
      state { name }
    }
  }
}
`

const queryViewer = `
query Viewer {
  viewer {
    id
  }
}
`

const queryIssueTeamByID = `
query IssueTeamByID($issueID: String!) {
  issue(id: $issueID) {
    id
    team { id }
  }
}
`

const queryWorkflowStateByName = `
query WorkflowStateByName($teamID: String!, $stateName: String!) {
  workflowStates(
    filter: {
      team: { id: { eq: $teamID } }
      name: { eq: $stateName }
    }
    first: 10
  ) {
    nodes {
      id
      name
    }
  }
}
`

const mutationIssueUpdateState = `
mutation IssueUpdateState($issueID: String!, $stateID: String!) {
  issueUpdate(id: $issueID, input: { stateId: $stateID }) {
    success
    issue { id state { name } }
  }
}
`

const mutationCommentCreate = `
mutation CommentCreate($issueID: String!, $body: String!) {
  commentCreate(input: { issueId: $issueID, body: $body }) {
    success
    comment { id }
  }
}
`
