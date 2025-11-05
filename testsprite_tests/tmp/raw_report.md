
# TestSprite AI Testing Report(MCP)

---

## 1️⃣ Document Metadata
- **Project Name:** stack_service
- **Date:** 2025-10-03
- **Prepared by:** TestSprite AI Team

---

## 2️⃣ Requirement Validation Summary

#### Test TC001
- **Test Name:** user registration with valid and invalid inputs
- **Test Code:** [TC001_user_registration_with_valid_and_invalid_inputs.py](./TC001_user_registration_with_valid_and_invalid_inputs.py)
- **Test Error:** Traceback (most recent call last):
  File "/var/task/handler.py", line 258, in run_with_retry
    exec(code, exec_env)
  File "<string>", line 58, in <module>
  File "<string>", line 17, in test_user_registration_with_valid_and_invalid_inputs
AssertionError: Expected 201 Created, got 501, content: {"error":"Not implemented yet","message":"User registration endpoint will be implemented"}

- **Test Visualization and Result:** https://www.testsprite.com/dashboard/mcp/tests/3932e877-9747-4939-85da-64e9610e18ae/f4baeaf6-6a4e-42df-8614-70542c86fe65
- **Status:** ❌ Failed
- **Analysis / Findings:** {{TODO:AI_ANALYSIS}}.
---

#### Test TC002
- **Test Name:** user login with correct and incorrect credentials
- **Test Code:** [TC002_user_login_with_correct_and_incorrect_credentials.py](./TC002_user_login_with_correct_and_incorrect_credentials.py)
- **Test Error:** Traceback (most recent call last):
  File "/var/task/handler.py", line 258, in run_with_retry
    exec(code, exec_env)
  File "<string>", line 85, in <module>
  File "<string>", line 26, in test_user_login_with_correct_and_incorrect_credentials
AssertionError: Unexpected register status 501, body: {"error":"Not implemented yet","message":"User registration endpoint will be implemented"}

- **Test Visualization and Result:** https://www.testsprite.com/dashboard/mcp/tests/3932e877-9747-4939-85da-64e9610e18ae/1b258aec-6647-4691-940c-6d95828bd998
- **Status:** ❌ Failed
- **Analysis / Findings:** {{TODO:AI_ANALYSIS}}.
---

#### Test TC003
- **Test Name:** start onboarding process with valid and invalid data
- **Test Code:** [TC003_start_onboarding_process_with_valid_and_invalid_data.py](./TC003_start_onboarding_process_with_valid_and_invalid_data.py)
- **Test Error:** Traceback (most recent call last):
  File "/var/task/handler.py", line 258, in run_with_retry
    exec(code, exec_env)
  File "<string>", line 84, in <module>
  File "<string>", line 35, in test_start_onboarding_process_with_valid_and_invalid_data
AssertionError: Expected 201, got 404 with body 404 page not found

- **Test Visualization and Result:** https://www.testsprite.com/dashboard/mcp/tests/3932e877-9747-4939-85da-64e9610e18ae/85ad1a9f-901f-4073-ae53-6993842e645c
- **Status:** ❌ Failed
- **Analysis / Findings:** {{TODO:AI_ANALYSIS}}.
---

#### Test TC004
- **Test Name:** retrieve onboarding status with valid and invalid user ids
- **Test Code:** [TC004_retrieve_onboarding_status_with_valid_and_invalid_user_ids.py](./TC004_retrieve_onboarding_status_with_valid_and_invalid_user_ids.py)
- **Test Error:** Traceback (most recent call last):
  File "/var/task/handler.py", line 258, in run_with_retry
    exec(code, exec_env)
  File "<string>", line 97, in <module>
  File "<string>", line 21, in test_retrieve_onboarding_status_with_valid_and_invalid_user_ids
AssertionError: Onboarding start failed: 404 page not found

- **Test Visualization and Result:** https://www.testsprite.com/dashboard/mcp/tests/3932e877-9747-4939-85da-64e9610e18ae/e9732170-b6d8-496d-892d-d14f3e929c01
- **Status:** ❌ Failed
- **Analysis / Findings:** {{TODO:AI_ANALYSIS}}.
---

#### Test TC005
- **Test Name:** submit kyc documents with complete and incomplete data
- **Test Code:** [TC005_submit_kyc_documents_with_complete_and_incomplete_data.py](./TC005_submit_kyc_documents_with_complete_and_incomplete_data.py)
- **Test Error:** Traceback (most recent call last):
  File "/var/task/handler.py", line 258, in run_with_retry
    exec(code, exec_env)
  File "<string>", line 191, in <module>
  File "<string>", line 88, in test_submit_kyc_documents_complete_incomplete
  File "<string>", line 28, in register_and_start_onboarding
AssertionError: Registration failed: {"error":"Not implemented yet","message":"User registration endpoint will be implemented"}

- **Test Visualization and Result:** https://www.testsprite.com/dashboard/mcp/tests/3932e877-9747-4939-85da-64e9610e18ae/8637b4b4-62f8-42ef-af4b-605281dec2f0
- **Status:** ❌ Failed
- **Analysis / Findings:** {{TODO:AI_ANALYSIS}}.
---

#### Test TC006
- **Test Name:** process kyc callback with valid and invalid payloads
- **Test Code:** [TC006_process_kyc_callback_with_valid_and_invalid_payloads.py](./TC006_process_kyc_callback_with_valid_and_invalid_payloads.py)
- **Test Error:** Traceback (most recent call last):
  File "/var/task/handler.py", line 258, in run_with_retry
    exec(code, exec_env)
  File "<string>", line 158, in <module>
  File "<string>", line 19, in test_process_kyc_callback_with_valid_and_invalid_payloads
AssertionError: User registration failed: {"error":"Not implemented yet","message":"User registration endpoint will be implemented"}

- **Test Visualization and Result:** https://www.testsprite.com/dashboard/mcp/tests/3932e877-9747-4939-85da-64e9610e18ae/e366ec63-6cb7-46b8-a75a-dd78ba23482c
- **Status:** ❌ Failed
- **Analysis / Findings:** {{TODO:AI_ANALYSIS}}.
---

#### Test TC007
- **Test Name:** get wallet addresses filtered by blockchain chain
- **Test Code:** [TC007_get_wallet_addresses_filtered_by_blockchain_chain.py](./TC007_get_wallet_addresses_filtered_by_blockchain_chain.py)
- **Test Error:** Traceback (most recent call last):
  File "/var/task/handler.py", line 258, in run_with_retry
    exec(code, exec_env)
  File "<string>", line 147, in <module>
  File "<string>", line 26, in test_get_wallet_addresses_filtered_by_blockchain_chain
AssertionError: Unexpected register status code: 501 {"error":"Not implemented yet","message":"User registration endpoint will be implemented"}

- **Test Visualization and Result:** https://www.testsprite.com/dashboard/mcp/tests/3932e877-9747-4939-85da-64e9610e18ae/0bcbf165-f028-40ae-bc68-8bccb4aadc71
- **Status:** ❌ Failed
- **Analysis / Findings:** {{TODO:AI_ANALYSIS}}.
---

#### Test TC008
- **Test Name:** get wallet status with valid and invalid user context
- **Test Code:** [TC008_get_wallet_status_with_valid_and_invalid_user_context.py](./TC008_get_wallet_status_with_valid_and_invalid_user_context.py)
- **Test Error:** Traceback (most recent call last):
  File "/var/task/handler.py", line 258, in run_with_retry
    exec(code, exec_env)
  File "<string>", line 167, in <module>
  File "<string>", line 114, in test_wallet_status_with_valid_and_invalid_user_context
  File "<string>", line 21, in register_and_onboard_user
AssertionError: Unexpected register status: 501

- **Test Visualization and Result:** https://www.testsprite.com/dashboard/mcp/tests/3932e877-9747-4939-85da-64e9610e18ae/3ed08f4c-578c-46b5-a567-093ed531a589
- **Status:** ❌ Failed
- **Analysis / Findings:** {{TODO:AI_ANALYSIS}}.
---

#### Test TC009
- **Test Name:** admin create wallets for user with valid and invalid inputs
- **Test Code:** [TC009_admin_create_wallets_for_user_with_valid_and_invalid_inputs.py](./TC009_admin_create_wallets_for_user_with_valid_and_invalid_inputs.py)
- **Test Error:** Traceback (most recent call last):
  File "/var/task/handler.py", line 258, in run_with_retry
    exec(code, exec_env)
  File "<string>", line 158, in <module>
  File "<string>", line 110, in test_admin_create_wallets_for_user_valid_invalid_inputs
  File "<string>", line 23, in register_user
AssertionError: Unexpected status code on registration: 501

- **Test Visualization and Result:** https://www.testsprite.com/dashboard/mcp/tests/3932e877-9747-4939-85da-64e9610e18ae/3cf72821-35cc-49a7-869a-a71844fc9ff4
- **Status:** ❌ Failed
- **Analysis / Findings:** {{TODO:AI_ANALYSIS}}.
---

#### Test TC010
- **Test Name:** generate deposit address for supported blockchain chains
- **Test Code:** [TC010_generate_deposit_address_for_supported_blockchain_chains.py](./TC010_generate_deposit_address_for_supported_blockchain_chains.py)
- **Test Error:** Traceback (most recent call last):
  File "/var/task/handler.py", line 258, in run_with_retry
    exec(code, exec_env)
  File "<string>", line 122, in <module>
  File "<string>", line 19, in test_generate_deposit_address_for_supported_blockchains
AssertionError: Registration failed: {"error":"Not implemented yet","message":"User registration endpoint will be implemented"}

- **Test Visualization and Result:** https://www.testsprite.com/dashboard/mcp/tests/3932e877-9747-4939-85da-64e9610e18ae/a908e4d8-de81-4558-8bcc-86afbfaf286c
- **Status:** ❌ Failed
- **Analysis / Findings:** {{TODO:AI_ANALYSIS}}.
---


## 3️⃣ Coverage & Matching Metrics

- **0.00** of tests passed

| Requirement        | Total Tests | ✅ Passed | ❌ Failed  |
|--------------------|-------------|-----------|------------|
| ...                | ...         | ...       | ...        |
---


## 4️⃣ Key Gaps / Risks
{AI_GNERATED_KET_GAPS_AND_RISKS}
---