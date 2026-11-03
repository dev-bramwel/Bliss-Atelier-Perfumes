SELECT c.relname, jsonb_build_object(
 'kind', c.relkind, 'persistence', c.relpersistence,
 'rls', c.relrowsecurity, 'force_rls', c.relforcerowsecurity,
 'options', c.reloptions, 'partition', c.relispartition,
 'columns', (SELECT jsonb_agg(jsonb_build_array(a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull,
   pg_get_expr(d.adbin,d.adrelid),a.attidentity,a.attgenerated,co.collname) ORDER BY a.attnum)
   FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
   LEFT JOIN pg_collation co ON co.oid=a.attcollation
   WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped),
 'constraints', (SELECT jsonb_agg(jsonb_build_array(conname,pg_get_constraintdef(oid),convalidated) ORDER BY conname)
   FROM pg_constraint WHERE conrelid=c.oid),
 'indexes', (SELECT jsonb_agg(jsonb_build_array(ci.relname,pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready) ORDER BY ci.relname)
   FROM pg_index i JOIN pg_class ci ON ci.oid=i.indexrelid WHERE i.indrelid=c.oid),
 'triggers', (SELECT jsonb_agg(pg_get_triggerdef(oid) ORDER BY tgname) FROM pg_trigger WHERE tgrelid=c.oid AND NOT tgisinternal),
 'policies', (SELECT jsonb_agg(polname ORDER BY polname) FROM pg_policy WHERE polrelid=c.oid),
 'inherits', (SELECT count(*) FROM pg_inherits WHERE inhrelid=c.oid)
 )::text
FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='public' AND c.relkind NOT IN ('i','I') ORDER BY c.relname;
