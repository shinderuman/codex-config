# Task: worker repo-search integration

## Status

planned

## Original instruction

workerが対象不明時だけrepo-searchを使うproduction routingを実装し、毎回BM25を強制しない。

## Amendments

none

## Purpose

一次探索tokenを削減しつつ既知targetの無駄なsearchを避ける。

## Contract

- target不明条件とfallbackをmachine判定
- BM25 core/fingerprint修正は再実装しない

## Must not

- 全task強制search、外部search API、embeddingを追加しない

## Acceptance criteria

- production prompt/dispatch因果、known/unknown target scenario
- search failure fail-safeとtelemetry
- test/race/vet/build/gofmt、独立reviewer、Sol gate、commit

## Historical invariants

- BM25 coreとfingerprint `87fb116`

## Dependencies

- 001-requirement-task-lifecycle.md

## Review findings

none

## Current boundary

core実装済み、production routing未接続。
