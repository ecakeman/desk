# Third-party notices

## shadcn-admin

- Upstream: https://github.com/satnaing/shadcn-admin
- Branch: `main`
- Pinned commit: `e16c87f213a5ba5e45964e9b67c792105ec74d26`
- Commit date: 2026-06-11
- License: MIT

The Desk control-plane shell, theme tokens, compact sidebar treatment, and
dashboard visual direction are adapted from shadcn-admin. Authentication,
Clerk integration, user/product/statistics demo pages, charts, tables, and
their dependencies were intentionally not included. Desk uses its own file
routes (`/`, `/s/$sessionId`, `/s/$sessionId/r/$runId`) and the generated
`src/routeTree.gen.ts` from `@tanstack/router-plugin`.

The upstream MIT license is preserved verbatim in `web/LICENSE`.
