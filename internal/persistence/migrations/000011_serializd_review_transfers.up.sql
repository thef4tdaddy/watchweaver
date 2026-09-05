CREATE TABLE serializd_review_transfers (
    review_id INTEGER PRIMARY KEY REFERENCES reviews(id) ON UPDATE RESTRICT ON DELETE CASCADE,
    review_updated_at TEXT NOT NULL,
    transferred_at TEXT NOT NULL,
    destination TEXT NOT NULL DEFAULT 'serializd'
);

