CREATE TABLE public.bliss_schema_migrations (
 version bigint PRIMARY KEY,
 checksum text NOT NULL,
 origin text NOT NULL CHECK (origin IN ('empty', 'prisma')),
 applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
