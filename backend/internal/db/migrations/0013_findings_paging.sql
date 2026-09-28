-- Paging through all of a contract's findings, newest first (by id).
CREATE INDEX contract_violations_contract_id_idx ON contract_violations (contract_id, id DESC);
