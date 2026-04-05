---
sidebar_position: 9
title: Auth
description: API endpoints for OAuth consent and token hooks.
---

# Auth API

These endpoints are called by Ory Hydra during the OAuth flow. They are not typically called directly by users.

## Get Consent Request

```
GET /api/v1/auth/consent?consent_challenge={challenge}
```

Called by the Console to display the consent screen. Returns the scopes available for approval (A ∩ B).

### Response

```json
{
  "data": {
    "challenge": "consent_challenge_xyz",
    "client": {
      "name": "tutor-agent",
      "description": "AI tutor for student interactions"
    },
    "requested_scopes": [
      "mcp:tools:search_curriculum",
      "mcp:tools:generate_quiz",
      "llm:model:gemini-2.5-flash"
    ],
    "user": "student@school.edu"
  }
}
```

Only scopes in both the agent ceiling (A) and user ceiling (B) are returned.

## Accept/Reject Consent

```
POST /api/v1/auth/consent
```

### Request Body (Accept)

```json
{
  "challenge": "consent_challenge_xyz",
  "action": "accept",
  "granted_scopes": [
    "mcp:tools:search_curriculum",
    "llm:model:gemini-2.5-flash"
  ]
}
```

### Request Body (Reject)

```json
{
  "challenge": "consent_challenge_xyz",
  "action": "reject",
  "reason": "User declined"
}
```

### Response

```json
{
  "data": {
    "redirect_to": "https://aiplex.example.com/callback?code=..."
  }
}
```

## Token Hook (Internal)

```
POST /api/v1/auth/token-hook
```

Called by Ory Hydra before issuing each token. Injects the `act` claim with the agent's SPIFFE ID. Not called by external clients.

## Health

```
GET /api/v1/health
```

### Response

```json
{
  "status": "ok",
  "version": "0.1.0",
  "uptime": "72h15m"
}
```
