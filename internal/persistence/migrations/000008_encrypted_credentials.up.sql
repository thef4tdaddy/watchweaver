CREATE TABLE encrypted_credentials (
    integration TEXT NOT NULL CHECK (length(trim(integration)) > 0),
    credential_key TEXT NOT NULL CHECK (length(trim(credential_key)) > 0),
    ciphertext BLOB NOT NULL CHECK (length(ciphertext) > 0),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (integration, credential_key)
);
