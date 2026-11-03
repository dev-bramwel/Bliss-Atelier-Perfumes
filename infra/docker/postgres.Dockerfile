FROM postgres:17.11-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24
# Compose always starts as UID 70. The unused root-to-postgres switch helper
# embeds an obsolete Go runtime; remove it rather than suppress its findings.
USER root
RUN test -f /usr/local/bin/gosu && rm /usr/local/bin/gosu
USER 70:70
