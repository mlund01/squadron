ALTER TABLE session_message_parts ADD COLUMN IF NOT EXISTS document_data TEXT;
ALTER TABLE session_message_parts ADD COLUMN IF NOT EXISTS document_media_type TEXT;
ALTER TABLE session_message_parts ADD COLUMN IF NOT EXISTS document_filename TEXT;
