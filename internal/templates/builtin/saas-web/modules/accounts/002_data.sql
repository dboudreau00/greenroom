INSERT INTO accounts (name, segment, region, created_at) VALUES
  ('Tidewell Freight',  'mid-market', 'EMEA', now() - interval '420 days'),
  ('Quillmark Health',  'enterprise', 'NA',   now() - interval '380 days'),
  ('Brasswick Foods',   'mid-market', 'NA',   now() - interval '300 days'),
  ('Vantor Retail',     'enterprise', 'APAC', now() - interval '260 days'),
  ('Cobalt Reach',      'smb',        'NA',   now() - interval '190 days'),
  ('Fennick Energy',    'enterprise', 'EMEA', now() - interval '150 days'),
  ('Marrowind Media',   'smb',        'EMEA', now() - interval '90 days'),
  ('Halden Bikes',      'smb',        'NA',   now() - interval '30 days');

INSERT INTO users (account_id, email, role, last_seen_at) VALUES
  (1, 'ops@tidewell.example',        'admin',  now() - interval '2 hours'),
  (1, 'finance@tidewell.example',    'member', now() - interval '3 days'),
  (2, 'it@quillmark.example',        'admin',  now() - interval '1 hour'),
  (2, 'analyst@quillmark.example',   'member', now() - interval '5 hours'),
  (2, 'cfo@quillmark.example',       'viewer', now() - interval '9 days'),
  (3, 'owner@brasswick.example',     'admin',  now() - interval '1 day'),
  (4, 'platform@vantor.example',     'admin',  now() - interval '30 minutes'),
  (4, 'stores@vantor.example',       'member', now() - interval '6 hours'),
  (5, 'founder@cobaltreach.example', 'admin',  now() - interval '12 days'),
  (6, 'grid@fennick.example',        'admin',  now() - interval '4 hours'),
  (6, 'trading@fennick.example',     'member', now() - interval '2 days'),
  (7, 'studio@marrowind.example',    'admin',  now() - interval '20 days'),
  (8, 'shop@halden.example',         'admin',  now() - interval '3 hours'),
  (8, 'repairs@halden.example',      'member', now() - interval '1 day');
