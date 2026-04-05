---
sidebar_position: 5
title: Permissions
description: API endpoints for managing user and agent permissions.
---

# Permissions API

## Get User Scopes

```
GET /api/v1/users/{user_id}/scopes
```

Returns dimension B (user ceiling) for a user.

### Response

```json
{
  "data": {
    "user_id": "student@school.edu",
    "scopes": [
      "mcp:tools:search_curriculum",
      "mcp:tools:generate_quiz",
      "llm:model:gemini-2.5-flash"
    ]
  }
}
```

## Set User Scopes

```
PUT /api/v1/users/{user_id}/scopes
```

### Request Body

```json
{
  "scopes": [
    "mcp:tools:search_curriculum",
    "mcp:tools:generate_quiz",
    "mcp:tools:grade_assignment",
    "llm:model:gemini-2.5-flash"
  ]
}
```

### Response

```json
{
  "data": {
    "user_id": "student@school.edu",
    "scopes": ["mcp:tools:search_curriculum", "mcp:tools:generate_quiz", "mcp:tools:grade_assignment", "llm:model:gemini-2.5-flash"]
  },
  "message": "User scopes updated"
}
```
