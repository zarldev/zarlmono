import {test,expect} from '@playwright/test';

test('rejected checkpoints offer export instead of a futile retry',async({page})=>{
 await page.goto('/tests/harness.html?rejected');
 await page.getByRole('button',{name:'Connect',exact:true}).click();
 await expect(page.getByText('checkpoint was rejected; retry cannot repair it')).toBeVisible();
 await expect(page.getByRole('button',{name:'Retry save',exact:true})).toHaveCount(0);
 await page.getByRole('button',{name:'Export recovery copy'}).click();
 await expect(page.getByRole('status')).toContainText('/test/project/exports/recovery.md');
 await expect(page.getByRole('button',{name:'Send ↗'})).toBeDisabled();
});

test('transient save failures retain a working retry action',async({page})=>{
 await page.goto('/tests/harness.html?save-error');
 await page.getByRole('button',{name:'Connect',exact:true}).click();
 await page.getByRole('button',{name:'Retry save',exact:true}).click();
 await expect(page.getByRole('alert')).toHaveCount(0);
 await page.getByRole('textbox',{name:'Prompt',exact:true}).fill('continue');
 await expect(page.getByRole('button',{name:'Send ↗'})).toBeEnabled();
});
