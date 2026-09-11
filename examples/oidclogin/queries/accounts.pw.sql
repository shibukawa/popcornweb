package queries

type Account { id: string, display_name: string, email: string }

export statement FindAccount(issuer: string, claim: string, value: string): sql.optional<Account> {
  SELECT accounts.id, accounts.display_name, accounts.email
  FROM accounts
  JOIN external_identities
    ON external_identities.account_id = accounts.id
  WHERE
    external_identities.issuer = {issuer}
    AND external_identities.claim = {claim}
    AND external_identities.value = {value}
}

export statement InsertAccount(id: string, display_name: string, email: string): sql.exec {
  INSERT INTO accounts (id, display_name, email)
  VALUES ({id}, {display_name}, {email})
}

export statement LinkIdentity(issuer: string, claim: string, value: string, account_id: string): sql.exec {
  INSERT INTO external_identities (issuer, claim, value, account_id)
  VALUES ({issuer}, {claim}, {value}, {account_id})
}

export statement UpdateAccountProfile(id: string, display_name: string, email: string): sql.exec {
  UPDATE accounts
  SET display_name = {display_name}, email = {email}
  WHERE id = {id}
}
