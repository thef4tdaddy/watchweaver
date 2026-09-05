INSERT INTO app_settings(setting_key,setting_value) VALUES
    ('prompt_ratings_enabled','true'),
    ('prompt_reviews_enabled','true')
ON CONFLICT(setting_key) DO NOTHING;
