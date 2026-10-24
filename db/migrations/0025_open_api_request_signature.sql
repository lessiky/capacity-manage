-- M8 Task 10 Step 1: 开放 API 请求签名与重放保护
ALTER TABLE api_key
  ADD COLUMN IF NOT EXISTS signature_required boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN api_key.signature_required IS
  '启用后要求 X-Cap-Timestamp/X-Cap-Nonce/X-Cap-Signature 与 Redis nonce 防重放';
