# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.1.0] - 2026-05-12 — Agent harness

### Added

- Linear-as-control-plane migration: issues, state transitions, and agent
  handoffs now flow through Linear instead of GitHub Issues.
- Tart-VM-per-issue workspace primitive: each agent run gets an isolated
  macOS VM with the repo pre-cloned, so runs cannot clobber each other.
- Go orchestrator following the Symphony spec: picks up Ready issues,
  spawns a per-issue VM, runs the Claude Code agent, and reports back.
