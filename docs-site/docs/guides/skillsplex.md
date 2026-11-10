---
title: SkillsPlex
description: Discover deployed skills and inspect reported invocation history.
---

# SkillsPlex

SkillsPlex exposes deployed skills and records reported invocation history. It publishes skill manifests and capabilities, and keeps attributed audit records.

An invocation report does not execute a skill or prove authorization. The record shows what was reported, not what was enforced.

```bash
aiplex skills servers                     # running SkillsPlex servers
aiplex skills invocations                 # recent invocations
aiplex skills record-invocation --help    # record an invocation audit event
```

Each deployed skill instance serves a manifest at `/skills/{instanceId}/.well-known/skills.json`. Through an access point, exposed skills are listed at `GET /ap/{id}/skills`. SkillsPlex must be enabled in `AIPLEX_MODULES` for these routes.

Read [A2A and Skills discovery and reported history](../library/a2a-skills-audit.md) for the coordinates a record carries and the lifecycle.
