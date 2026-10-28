# ADR 004: Verified SQL baseline and migration ownership

Date: 21 October 2026. Status: selected for R06 implementation.

Use a repository-owned, PostgreSQL-only SQL runner in `backend/internal/migration`, built on the pinned pgx dependency. The runner embeds ordered SQL, records SHA-256 checksums, and holds transaction-scoped advisory locks across inspection and execution. All changes and ledger writes commit together. This deliberately small runner supports transactional SQL only, no down/reset/force operation, and no external migration directory supplied at runtime.

Atlas offers checksums, advisory locks and versioned migrations, but adds a separate executable/toolchain and revision-adoption integration. Goose/golang-migrate would still require application-specific Prisma verification and checksum policy. For the current single baseline, keep those guarantees in one auditable transaction using the existing pgx dependency. This choice adds maintenance responsibility: new migrations must extend the embedded ordered list and expected schema contract, and receive upgrade/failure tests. Reconsider an external runner when nontransactional or more complex migrations are required.

`init` accepts an empty public schema only. `adopt-legacy` accepts precisely the committed quoted Prisma schema with one successfully completed historical migration, an approved checksum, and no failed/unknown history. It preserves all Prisma history. Neither path infers ownership from table names alone. `up` requires an existing verified Go ledger; `status` inspects without permanent changes. Acquire both the Go lock and Prisma's advisory lock (72707369), failing immediately on contention. Operators must stop other schema tools and revoke DDL from application roles; advisory locks cannot restrain uncooperative administrators.

Stop automatic Prisma migration deployment in `npm start`; Prisma client generation remains. Legacy-only databases require an explicit Prisma deployment before Go adoption. Once adopted, use only the Go command. Express remains the application writer until the separate route cutover. Preserve the baseline tables, Prisma history and original SQL archive; no destructive down migration is provided.

Sources: [PostgreSQL advisory locks](https://www.postgresql.org/docs/17/explicit-locking.html#ADVISORY-LOCKS), [Atlas execution/checksums/locking](https://atlasgo.io/versioned/apply), [Prisma migration histories](https://www.prisma.io/docs/orm/prisma-migrate/understanding-prisma-migrate/migration-histories).
