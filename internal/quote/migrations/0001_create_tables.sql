CREATE TABLE IF NOT EXISTS quotes (
    ticker   varchar(12) NOT NULL,
    date     date        NOT NULL,
    name     varchar(12),
    spec     varchar(10),
    isin     varchar(12),
    open     numeric(15,2),
    high     numeric(15,2),
    low      numeric(15,2),
    average  numeric(15,2),
    close    numeric(15,2),
    trades   bigint,
    quantity bigint,
    volume   numeric(20,2),
    CONSTRAINT quotes_pkey PRIMARY KEY (ticker, date)
);

CREATE TABLE IF NOT EXISTS synced_files (
    name      varchar(32) NOT NULL,
    quotes    bigint,
    synced_at timestamptz,
    CONSTRAINT synced_files_pkey PRIMARY KEY (name)
);
