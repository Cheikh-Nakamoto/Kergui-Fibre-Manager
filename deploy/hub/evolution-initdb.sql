-- Evolution Go keeps its users in a second database (POSTGRES_USERS_DB).
-- Run once, when the evolution-db volume is first initialised.
CREATE DATABASE evogo_users OWNER evogo;
