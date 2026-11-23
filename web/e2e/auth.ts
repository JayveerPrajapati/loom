import { expect, type APIRequestContext } from '@playwright/test';

// Set by playwright.config.ts for each run; never a literal credential.
export const e2eUser = process.env.LOOM_E2E_USER ?? '';
export const e2eCode = process.env.LOOM_E2E_CODE ?? '';

export async function login(request: APIRequestContext): Promise<void> {
  const response = await request.post('/api/auth/login', {
    data: { email: e2eUser, password: e2eCode },
  });
  expect(response.status(), await response.text()).toBe(200);
}
