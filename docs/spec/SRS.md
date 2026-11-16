# Software Requirements Specification

What the system must do, module by module. Each requirement has an ID so code, tests and tasks can refer to it.

## 1. Introduction

**1.1 Purpose.** This document specifies the Business Management System (BMS): a web system for running sales, stock, purchasing, money, staff and reporting for a business with one or more branches. It is the contract between what the business needs and what is built.

**1.2 Scope.** In scope: dashboard, sales and POS, products and inventory, customers, suppliers, purchases, expenses, payments and finance, employees, attendance, payroll, branches, users and roles, reports, notifications, security and audit, settings. Out of scope for version 1: e-commerce storefront, manufacturing, full statutory accounting (balance sheet filing), native mobile apps, automatic mobile money and bank integrations, offline POS.

**1.3 Technology decided.** Backend Go with Fiber v3, frontend React built with Vite, database PostgreSQL.

**1.4 Definitions**

| Term | Meaning |
| --- | --- |
| Business | The tenant. One company using the system. All data belongs to exactly one business |
| Branch | A shop, store or office of a business. Stock, tills and staff belong to a branch |
| Owner | Super admin of a business. Full access |
| Manager | Runs one or more branches. Sensitive access can be restricted by the Owner |
| Staff / Cashier | Sells at the POS and sees only their own records |
| Document | A business record with a number and a status: sale, purchase order, goods receipt, transfer, expense, payroll run |
| Posted | A document that has changed stock or money. It can no longer be edited, only voided or reversed |
| Stock movement | One signed quantity change of one product in one branch |
| Journal | The internal double-entry ledger that all money events are written to |
| Money account | A place money sits: cash drawer, bank account, mobile money wallet, petty cash |
| Till session | A cashier's shift on a cash drawer, from opening float to closing count |
| Allocation | Linking a payment to the invoices or sales it pays |
| GRN | Goods received note |
| PO | Purchase order |

**1.5 Requirement IDs.** `FR-<module>-<number>` for functional, `NFR-<number>` for non-functional, `BR-<number>` for business rules. Priority M means must have in version 1, S means should have, C means could have later.

## 2. Overall description

**2.1 Product perspective.** BMS is a self-contained multi-tenant web application. One deployment serves many businesses, each isolated from the others. It replaces paper books, spreadsheets and separate POS tools with one source of truth.

**2.2 User classes**

| User | Typical tasks | Skill level | Device |
| --- | --- | --- | --- |
| Owner / Super Admin | Sets up the business, watches profit and cash, approves payroll and large spending, manages users | Business literate, not technical | Laptop and phone |
| Manager | Receives stock, approves expenses and refunds, manages staff and attendance, closes the day | Comfortable with computers | Laptop or tablet |
| Staff / Cashier | Sells, takes payments, clocks in, views own payslip | Basic. Must be usable after 30 minutes of training | Shop PC, tablet or phone, barcode scanner, receipt printer |

**2.3 Operating environment**

- Server: Linux, Docker, Go binary, PostgreSQL 16 or newer, reverse proxy with HTTPS.
- Client: current Chrome, Edge, Firefox or Safari. Screen width from 360 px (phone) to desktop. POS optimised for 1024 px and wider.
- Peripherals: USB or Bluetooth barcode scanner acting as keyboard, 80 mm thermal receipt printer through the browser print dialog.
- Network: must stay usable on a slow mobile connection. Pages load data in small pages, not whole tables.

**2.4 Design and implementation constraints**

- Backend in Go with Fiber v3. Frontend in React with Vite and TypeScript. PostgreSQL is the only database.
- All amounts in one base currency per business. Default currency TZS, configurable.
- Interface in English and Kiswahili.
- All business rules enforced on the server. The frontend is never trusted.

**2.5 Assumptions and dependencies**

- Each business has internet at the point of sale in version 1.
- Mobile money and bank payments are confirmed by a person who types the reference number.
- Tax is a configurable percentage per product or per sale. Fiscal device integration is a later add-on behind an interface.
- Statutory payroll deductions are set up as configurable components by the business.
- Email or SMS delivery, if used, depends on a third-party gateway chosen later.

## 3. Functional requirements

Sections 3.1 to 3.19 cover every item in the menu tree. Pri is priority: M must, S should, C could.

### 3.1 Authentication (AUTH)

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-AUTH-01 | A user logs in with phone number or email and password | M |
| FR-AUTH-02 | A new business registers with business name, owner name, phone, password. The system creates the business, a main branch, the three default roles, the chart of accounts and the Owner user in one step | M |
| FR-AUTH-03 | The system issues a short-lived access token and a rotating refresh token. Logout revokes the refresh token | M |
| FR-AUTH-04 | After 5 failed logins in 15 minutes the account is locked for 15 minutes and the Owner is notified | M |
| FR-AUTH-05 | A user can change their password. An Owner or permitted Manager can reset another user's password | M |
| FR-AUTH-06 | A cashier can unlock the POS with a 4 to 6 digit PIN on a device where a full login already happened | S |
| FR-AUTH-07 | A user can see and end their own active sessions. An Owner can end any user's sessions | S |
| FR-AUTH-08 | A deactivated user cannot log in and all their sessions end immediately | M |
| FR-AUTH-09 | Forgot password by one-time code sent to phone or email | C |

### 3.2 Users and roles (USR)

Covers menu: Users, Roles, Permissions, Login History, Activity / Audit Logs.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-USR-01 | Create, edit, deactivate and reactivate users with name, phone, email, role, branches and optional link to an employee | M |
| FR-USR-02 | Three system roles exist by default: Owner, Manager, Staff / Cashier. System roles cannot be deleted | M |
| FR-USR-03 | The Owner can create custom roles and choose permissions for them | S |
| FR-USR-04 | Each permission on a role has a scope: all branches, own branch, or own records | M |
| FR-USR-05 | The Owner can switch sensitive permissions on or off for the Manager role: view cost and profit, view bank and mobile money balances, prepare payroll, view salaries, manage users, view financial reports, view audit logs | M |
| FR-USR-06 | Each role holds approval limits: maximum discount percent, maximum expense amount, maximum refund amount, maximum stock adjustment value, maximum purchase order value | M |
| FR-USR-07 | A user is assigned one or more branches and has one active branch at a time, switchable from the header | M |
| FR-USR-08 | Login history lists user, time, IP, device and result, filterable by user and date | M |
| FR-USR-09 | There must always be at least one active Owner. The last Owner cannot be deactivated or demoted | M |
| FR-USR-10 | A user cannot change their own role or permissions | M |

### 3.3 Business settings (SET)

Covers menu: Business Profile, Tax Settings, Currency, Payment Methods, Invoice Settings, Receipt Settings, Payroll Settings, Notification Settings, System Settings.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-SET-01 | Business profile: name, logo, address, phone, email, tax identification number, registration number | M |
| FR-SET-02 | Tax settings: named tax rates with percentage, whether prices include tax, default rate for new products | M |
| FR-SET-03 | Currency: base currency code, symbol, decimal places, thousands separator | M |
| FR-SET-04 | Payment methods: enable or disable cash, bank, mobile money per provider, credit. Each method maps to a money account | M |
| FR-SET-05 | Invoice settings: number prefix per branch, payment terms text, footer, default due days | M |
| FR-SET-06 | Receipt settings: header, footer, show logo, paper width 58 or 80 mm, show cashier name, show tax breakdown | M |
| FR-SET-07 | Payroll settings: pay day, working days per month, standard hours per day, overtime multiplier, late grace minutes, whether lateness deducts pay | M |
| FR-SET-08 | Notification settings: which events notify which roles, and through which channel | S |
| FR-SET-09 | System settings: timezone, date format, language default, allow negative stock, business day start time, session timeout | M |
| FR-SET-10 | Every settings change is written to the audit log with old and new value | M |

### 3.4 Branches (BRN)

Covers menu: Branch List, Branch Staff, Branch Inventory, Branch Sales, Branch Expenses, Stock Transfers, Branch Performance.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-BRN-01 | Create, edit, deactivate branches with name, short code, address, phone, manager | M |
| FR-BRN-02 | A branch with stock, open tills or unposted documents cannot be deactivated | M |
| FR-BRN-03 | Branch staff: list of employees and users assigned to the branch | M |
| FR-BRN-04 | Branch inventory, sales and expenses are the standard screens filtered to that branch | M |
| FR-BRN-05 | Stock transfers between branches follow FR-INV-20 to FR-INV-25 | M |
| FR-BRN-06 | Branch performance compares branches for a period: sales, gross profit, expenses, net profit, stock value, number of sales, average sale | M |
| FR-BRN-07 | The Owner can view all branches together or pick one. A Manager sees only assigned branches | M |

### 3.5 Products and inventory (INV)

Covers menu: Products, Categories, Stock In, Stock Out, Stock Transfer, Stock Adjustment, Low Stock Alerts, Inventory Valuation.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-INV-01 | Create and edit a product with name, SKU, barcode, category, unit of measure, selling price, tax rate, reorder level, image, active flag | M |
| FR-INV-02 | SKU and barcode are unique within a business. SKU is generated if left empty | M |
| FR-INV-03 | A product is either a stock item (quantity tracked) or a service (no stock) | M |
| FR-INV-04 | Selling price can be overridden per branch | S |
| FR-INV-05 | Search products by name, SKU or barcode. Barcode scan returns one exact match | M |
| FR-INV-06 | Import products from a CSV or Excel file with a validation report before saving | S |
| FR-INV-07 | A product used in any document cannot be deleted, only deactivated | M |
| FR-INV-08 | Cost price and margin are visible only to users with the cost permission | M |
| FR-INV-09 | Every price change is logged with old price, new price, user and time | M |
| FR-INV-10 | Create, edit and nest product categories. A category with products cannot be deleted | M |
| FR-INV-11 | Units of measure are a managed list: piece, kg, litre, box, carton | M |
| FR-INV-12 | Stock In: record stock received without a purchase order (opening stock, gift, found stock) with product, quantity, unit cost and reason | M |
| FR-INV-13 | Stock In updates quantity on hand and recalculates the weighted average cost | M |
| FR-INV-14 | Stock Out: record stock removed without a sale (damaged, expired, internal use, theft) with product, quantity and reason | M |
| FR-INV-15 | Stock Out posts the cost to an expense account matching the reason | M |
| FR-INV-16 | Stock Adjustment: enter counted quantity for products. The system calculates the difference | M |
| FR-INV-17 | An adjustment whose value is above the user's limit goes to approval before posting | M |
| FR-INV-18 | A posted adjustment writes one movement per product and a journal entry for the gain or loss | M |
| FR-INV-19 | Stock take: generate a count sheet for a branch or category, enter counts, and create one adjustment from it | S |
| FR-INV-20 | Stock Transfer: create a transfer from one branch to another with products and quantities | M |
| FR-INV-21 | Dispatch removes stock from the sending branch and marks it in transit | M |
| FR-INV-22 | The receiving branch confirms received quantity per product | M |
| FR-INV-23 | Received stock enters the receiving branch at the cost it left the sender | M |
| FR-INV-24 | Any difference between sent and received is recorded as transfer loss and notified to the Owner | M |
| FR-INV-25 | A draft transfer can be cancelled. A dispatched transfer can only be received or returned in full | M |
| FR-INV-26 | Low Stock Alerts: list products at or below reorder level per branch, with quantity on hand and suggested order quantity | M |
| FR-INV-27 | A notification is raised when a product crosses its reorder level, once until it is restocked | M |
| FR-INV-28 | Inventory Valuation: quantity, average cost and total value per product, per category and per branch, at today or at a past date | M |
| FR-INV-29 | Stock card: full movement history of one product in one branch with running balance | M |
| FR-INV-30 | Stock on hand can never be typed directly. It only changes through a movement | M |
| FR-INV-31 | If negative stock is disallowed in settings, any operation that would take stock below zero is rejected with a clear message | M |
| FR-INV-32 | Expiry date and batch number per stock receipt | C |

### 3.6 Sales / POS (SAL)

Covers menu: New Sale, Invoices, Receipts, Returns / Refunds, Discounts, Sales History.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-SAL-01 | New Sale: add products to a cart by barcode scan, search or category grid. Change quantity, remove lines | M |
| FR-SAL-02 | The server calculates line totals, discounts, tax and grand total. Prices come from the server, not the client | M |
| FR-SAL-03 | A cashier must have an open till session to take cash | M |
| FR-SAL-04 | Open a till session with an opening float. Close it with a counted amount. The system shows expected cash, counted cash and difference | M |
| FR-SAL-05 | Accept one or several payment methods on one sale: cash, mobile money with reference, bank with reference | M |
| FR-SAL-06 | For cash, show change due | M |
| FR-SAL-07 | Credit sale: a selected customer may pay nothing or part now. The unpaid part becomes customer debt, within their credit limit | M |
| FR-SAL-08 | Discounts: per line or on the whole sale, as amount or percent. Above the user's limit the sale waits for a Manager to approve on the spot with PIN or from their own screen | M |
| FR-SAL-09 | Hold a cart and resume it later. Held carts do not reserve stock | S |
| FR-SAL-10 | Completing a sale deducts stock, records payment and posts to the journal in one transaction | M |
| FR-SAL-11 | Receipts: print on 58 or 80 mm paper with business header, items, tax, total, paid, change, cashier, date and receipt number. Reprint is marked COPY | M |
| FR-SAL-12 | Invoices: an A4 invoice for a credit or business sale with customer details, due date and balance. Download as PDF | M |
| FR-SAL-13 | Quotation: save a priced cart as a quotation with no stock or money effect, and convert it to a sale later | C |
| FR-SAL-14 | Returns / Refunds: select a past sale, choose lines and quantities to return, give a reason | M |
| FR-SAL-15 | A return puts stock back (unless marked damaged), and refunds by cash, original method, or reduces the customer's debt | M |
| FR-SAL-16 | Returned quantity can never exceed sold quantity minus earlier returns | M |
| FR-SAL-17 | A cashier requests a refund. A Manager or Owner approves it according to limits | M |
| FR-SAL-18 | Void: a sale can be voided the same business day by a permitted user. Void reverses stock, payment and journal entries | M |
| FR-SAL-19 | Sales History: list and search by date, number, customer, cashier, status, payment method. Cashiers see only their own | M |
| FR-SAL-20 | Sale detail shows items, payments, returns, who sold, who approved and the audit trail | M |
| FR-SAL-21 | A retried submission with the same idempotency key returns the original sale and creates nothing new | M |
| FR-SAL-22 | Each sale line stores the cost at sale time so profit is fixed forever | M |
| FR-SAL-23 | Commission: if the selling employee has a commission rule, the sale records the commission base for payroll | S |

### 3.7 Customers (CUS)

Covers menu: Customer List, Customer Accounts, Credit / Debt, Customer Payments, Customer Statements.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-CUS-01 | Customer List: create, edit, deactivate customers with name, phone, email, address, tax number, notes | M |
| FR-CUS-02 | Phone number is unique per business when given. A walk-in customer exists by default for anonymous sales | M |
| FR-CUS-03 | Customer Accounts: per customer show total purchases, total paid, current balance, credit limit and last activity | M |
| FR-CUS-04 | Credit / Debt: set a credit limit and payment terms in days. A sale that would exceed the limit is blocked unless approved | M |
| FR-CUS-05 | Debt list: all customers who owe, with amount and age buckets 0 to 30, 31 to 60, 61 to 90, over 90 days | M |
| FR-CUS-06 | Customer Payments: record a payment from a customer with amount, method, reference and date | M |
| FR-CUS-07 | A payment is allocated to unpaid sales oldest first, or by hand. Any extra stays as customer credit for future sales | M |
| FR-CUS-08 | A payment receipt can be printed | M |
| FR-CUS-09 | Customer Statements: all sales, returns and payments for a period with opening balance, running balance and closing balance. Print or PDF | M |
| FR-CUS-10 | A customer with a balance or history cannot be deleted | M |
| FR-CUS-11 | Debt reminder notification when an invoice passes its due date | S |
| FR-CUS-12 | Write off a bad debt, Owner only, posting to a bad debt expense account | S |

### 3.8 Suppliers (SUP)

Covers menu: Supplier List, Purchases, Supplier Credit, Supplier Payments, Supplier Statements.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-SUP-01 | Supplier List: create, edit, deactivate suppliers with name, contact person, phone, email, address, tax number, payment terms in days | M |
| FR-SUP-02 | Purchases: per supplier, list all purchase orders, receipts and invoices | M |
| FR-SUP-03 | Supplier Credit: per supplier show total purchased, total paid, balance owed, and invoices by age bucket | M |
| FR-SUP-04 | Supplier Payments: record a payment to a supplier from a money account with amount, method, reference, date | M |
| FR-SUP-05 | A supplier payment is allocated to unpaid purchase invoices oldest first or by hand. Extra stays as an advance to the supplier | M |
| FR-SUP-06 | Supplier Statements: invoices, returns and payments for a period with running balance. Print or PDF | M |
| FR-SUP-07 | A supplier with history cannot be deleted | M |
| FR-SUP-08 | Notification when a supplier invoice is due within a set number of days or overdue | S |

### 3.9 Purchases (PUR)

Covers menu: Purchase Orders, Goods Received, Purchase Invoices, Returns, Purchase History.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-PUR-01 | Purchase Orders: create a PO for a supplier and branch with products, quantities, unit costs, expected date, notes | M |
| FR-PUR-02 | A PO above the user's limit needs approval before it can be received against | M |
| FR-PUR-03 | A PO can be printed or sent as PDF | S |
| FR-PUR-04 | A draft PO can be edited or cancelled. An approved PO can be cancelled only if nothing was received | M |
| FR-PUR-05 | Goods Received: record a receipt against a PO, entering quantity received and actual unit cost per line. Partial receipts are allowed | M |
| FR-PUR-06 | Direct receipt without a PO is allowed for small shops and creates the PO and receipt together | M |
| FR-PUR-07 | Posting a receipt adds stock and recalculates weighted average cost | M |
| FR-PUR-08 | Extra costs (transport, clearing) can be added to a receipt and spread across lines by value | S |
| FR-PUR-09 | Purchase Invoices: record the supplier's invoice for one or more receipts with invoice number, date, due date, amounts and tax | M |
| FR-PUR-10 | A purchase invoice may be paid in full on the spot (cash purchase) or left as supplier debt | M |
| FR-PUR-11 | Returns: return received goods to a supplier with reason. Stock is removed and the supplier balance is reduced or a refund is recorded | M |
| FR-PUR-12 | Purchase History: list and search all purchases by date, supplier, branch, status, product | M |
| FR-PUR-13 | A PO shows ordered, received and outstanding quantity per line | M |
| FR-PUR-14 | The system suggests a PO from products below reorder level for a chosen supplier | C |

### 3.10 Expenses (EXP)

Covers menu: Business Expenses, Expense Categories, Petty Cash, Recurring Expenses, Expense Approval.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-EXP-01 | Business Expenses: record an expense with date, category, amount, description, branch, paid-from account, payee, receipt photo | M |
| FR-EXP-02 | Expense Categories: managed list such as rent, utilities, transport, salaries, repairs, marketing. Each maps to a ledger expense account | M |
| FR-EXP-03 | Expense Approval: an expense above the submitter's limit waits for approval by Manager or Owner. Approver can approve or reject with a note | M |
| FR-EXP-04 | An approved expense is paid from a money account. Only then does cash go down | M |
| FR-EXP-05 | A paid expense cannot be edited. It can be reversed with a reason by a permitted user | M |
| FR-EXP-06 | Petty Cash: a petty cash account per branch with a custodian. Top up from another account, spend through expenses, and reconcile by count | M |
| FR-EXP-07 | Petty cash balance can never go below zero | M |
| FR-EXP-08 | Recurring Expenses: a template with amount, category, frequency (weekly, monthly, yearly), start and end date | M |
| FR-EXP-09 | On the due date the system creates the expense in submitted state and notifies the approver | M |
| FR-EXP-10 | Expense list filterable by date, category, branch, status, user | M |
| FR-EXP-11 | Staff see only expenses they submitted | M |
| FR-EXP-12 | An expense can be owed to a supplier instead of paid now, creating supplier debt | C |

### 3.11 Payments and finance (FIN)

Covers menu: Cash, Bank Accounts, Mobile Money, Customer Payments, Supplier Payments, Staff Payments, Transfers, Income, Expenses, Transactions History.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-FIN-01 | Cash: one or more cash accounts per branch (till, safe). Show current balance and movements | M |
| FR-FIN-02 | Bank Accounts: create accounts with bank name, account name, account number, opening balance. Show balance and movements | M |
| FR-FIN-03 | Mobile Money: create wallets per provider with number, opening balance. Show balance and movements | M |
| FR-FIN-04 | Every money account has a type, an optional branch, an active flag, and a matching ledger account created automatically | M |
| FR-FIN-05 | Opening balances are posted once against an opening equity account | M |
| FR-FIN-06 | Customer Payments: list, create and view payments received from customers (same records as FR-CUS-06) | M |
| FR-FIN-07 | Supplier Payments: list, create and view payments to suppliers (same records as FR-SUP-04) | M |
| FR-FIN-08 | Staff Payments: list salary payments, advances, loan disbursements and other payments to employees | M |
| FR-FIN-09 | Transfers: move money between two money accounts with amount, date, reference and optional fee. Both balances change together | M |
| FR-FIN-10 | Cash banking: record a deposit from a till or safe to a bank or mobile wallet as a transfer | M |
| FR-FIN-11 | Income: record non-sales income such as rent received, commission, interest, owner capital injection, with category and account | M |
| FR-FIN-12 | Owner drawings: record money taken out by the owner, Owner only | S |
| FR-FIN-13 | Expenses: read-only list of paid expenses with link to the expense record | M |
| FR-FIN-14 | Transactions History: one list of every money movement across all accounts with date, type, party, account, in, out, running balance, reference, user. Filter by account, type, date, branch | M |
| FR-FIN-15 | A payment can be voided by a permitted user with a reason. Voiding posts a reversing entry and reopens the invoices it paid | M |
| FR-FIN-16 | Account reconciliation: enter the real balance from a statement or a count. The system shows the difference and lets a permitted user post an adjustment with reason | S |
| FR-FIN-17 | A money account balance cannot go negative unless the account is marked as allowing overdraft | M |
| FR-FIN-18 | Bank and mobile money balances are hidden from users without the balance permission | M |
| FR-FIN-19 | Period close: the Owner can lock a month. No document dated inside a locked month can be posted, voided or reversed | M |
| FR-FIN-20 | Each payment prints a payment voucher or receipt with a unique number | M |
| FR-FIN-21 | All balances are computed from journal lines. No screen allows a balance to be typed | M |

### 3.12 Employees / staff (EMP)

Covers menu: Employee Profiles, Departments, Positions, Attendance, Leave, Loans / Advances, Commissions, Bonuses, Deductions, Payroll, Salary Payments, Employee Statements.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-EMP-01 | Employee Profiles: name, photo, phone, address, national ID reference, employee number, branch, department, position, hire date, employment type (permanent, contract, casual), pay type (monthly, daily, hourly), base pay, bank or mobile money details for salary, emergency contact, status | M |
| FR-EMP-02 | Salary fields are visible only to users with the salary permission and to the employee themselves | M |
| FR-EMP-03 | Terminate an employee with date and reason. Terminated employees stay in history and are excluded from future payroll | M |
| FR-EMP-04 | Departments: managed list with optional head of department | M |
| FR-EMP-05 | Positions: managed list with optional default base pay | M |
| FR-EMP-06 | Attendance: the employee profile shows that employee's attendance summary from the Attendance module | M |
| FR-EMP-07 | Leave: leave types (annual, sick, maternity, unpaid) with days allowed per year. An employee requests leave with dates and reason | M |
| FR-EMP-08 | A Manager or Owner approves or rejects leave. Approved leave marks those attendance days as leave and reduces the balance | M |
| FR-EMP-09 | Unpaid leave days reduce pay in the payroll run | M |
| FR-EMP-10 | Loans / Advances: record a loan or salary advance with amount, date, paid-from account, and monthly deduction. An advance is a loan repaid in one deduction | M |
| FR-EMP-11 | A loan needs approval according to limits. Disbursement records a staff payment | M |
| FR-EMP-12 | Loan screen shows principal, repaid, balance and schedule. Repayments come from payroll deductions or direct cash repayment | M |
| FR-EMP-13 | Commissions: a rule per employee or position: percent of own sales, or fixed amount per sale, optionally above a target | S |
| FR-EMP-14 | Bonuses: add a one-off bonus to an employee for a payroll period with reason | M |
| FR-EMP-15 | Deductions: add a one-off deduction (shortage, damage, fine) for a payroll period with reason | M |
| FR-EMP-16 | Payroll and Salary Payments: the profile lists the employee's payslips and payments from the Payroll module | M |
| FR-EMP-17 | Employee Statements: for a period, show earnings, deductions, net pay, payments made, loans and balances | M |
| FR-EMP-18 | Self-service: an employee sees own profile, attendance, leave balance, loans, payslips and payment history, and nothing about others | M |
| FR-EMP-19 | Upload employee documents such as contract and ID copy | C |

### 3.13 Attendance and time (ATT)

Covers menu: Clock In / Out, Attendance History, Late Records, Overtime, Attendance Reports.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-ATT-01 | Clock In / Out: an employee clocks in and out from their own login. Server time is used, never device time | M |
| FR-ATT-02 | A Manager can clock staff in or out on a shared device, or enter a missed record with a reason. Manual entries are flagged | M |
| FR-ATT-03 | Work schedules: per branch or employee, working days, start time, end time, break minutes | M |
| FR-ATT-04 | Each day gets a status per employee: present, late, absent, half day, leave, holiday, day off | M |
| FR-ATT-05 | Attendance History: daily list with in time, out time, worked hours, status. Filter by employee, branch, date | M |
| FR-ATT-06 | Late Records: arrivals after start time plus grace minutes, with minutes late | M |
| FR-ATT-07 | Overtime: minutes worked beyond the schedule. Overtime counts for pay only after a Manager approves it | M |
| FR-ATT-08 | Public holidays are a managed calendar | S |
| FR-ATT-09 | A missing clock-out is flagged for the Manager to fix. It is never guessed silently | M |
| FR-ATT-10 | Attendance Reports: monthly summary per employee with days present, absent, late count, late minutes, leave days, overtime hours | M |
| FR-ATT-11 | Attendance for a period locks when its payroll run is approved | M |
| FR-ATT-12 | Optional restriction of clock-in to the branch network or location | C |

### 3.14 Payroll (PAY)

Covers menu: Salary Structure, Monthly Payroll, Allowances, Deductions, Overtime, Commission, Loans, Payslips, Salary Payment History.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-PAY-01 | Salary Structure: per employee, base pay plus a list of recurring components | M |
| FR-PAY-02 | Allowances: components such as housing, transport, meals. Fixed amount or percent of base. Flag taxable or not | M |
| FR-PAY-03 | Deductions: components such as income tax, social security, health insurance, union. Fixed, percent of base, percent of gross, or a bracket table | M |
| FR-PAY-04 | Employer contributions are components that cost the business but do not reduce net pay | S |
| FR-PAY-05 | Monthly Payroll: create a run for a period and branch or whole business. One run per period per scope | M |
| FR-PAY-06 | Calculate: for each active employee compute base pay (prorated for joiners, leavers, unpaid leave, absent days), allowances, overtime pay, commission, bonuses, gross, each deduction, loan repayment, net | M |
| FR-PAY-07 | Overtime: approved overtime hours times hourly rate times the multiplier from settings | M |
| FR-PAY-08 | Commission: sum of commission for the employee's completed sales in the period, net of returns | S |
| FR-PAY-09 | Loans: deduct the scheduled repayment, never more than the loan balance and never taking net pay below zero | M |
| FR-PAY-10 | A run can be recalculated while in draft or calculated state. Individual payslip lines can be adjusted with a reason before approval | M |
| FR-PAY-11 | Approve: Owner only by default. Approval freezes all payslips and posts the salary expense and liabilities to the journal | M |
| FR-PAY-12 | Pay: record salary payments per employee or in bulk from a chosen money account. Partial payment of a run is allowed | M |
| FR-PAY-13 | When all payslips are paid the run locks and loan balances are reduced | M |
| FR-PAY-14 | Payslips: one per employee per run with earnings, deductions, net, loan balance after. Print or PDF. An employee sees only their own | M |
| FR-PAY-15 | Salary Payment History: list of all salary payments with date, employee, amount, account, run | M |
| FR-PAY-16 | Bank or mobile money payment list export for a run as CSV | S |
| FR-PAY-17 | An approved run can only be undone by the Owner through a reversal that posts opposite entries, and only if no payment was made | M |
| FR-PAY-18 | Salary Due notification a set number of days before pay day and when a run awaits approval | M |

### 3.15 Approvals (APR)

Covers menu: Expense Approval, Transaction Approval, Approval Requests.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-APR-01 | These actions create an approval request when above the actor's limit: discount, refund, sale void, expense, stock adjustment, purchase order, employee loan, leave, overtime, payroll run, credit limit override, payment void | M |
| FR-APR-02 | A request shows what, who, amount, reason and a link to the record. It goes to users whose role may approve that type at that amount in that branch | M |
| FR-APR-03 | An approver approves or rejects with an optional note. The record's state changes in the same transaction | M |
| FR-APR-04 | Nobody can approve their own request, except the Owner | M |
| FR-APR-05 | Pending requests are listed in one inbox with count badge. History is searchable | M |
| FR-APR-06 | A requester can cancel a pending request | M |
| FR-APR-07 | At the POS a Manager can approve a discount or refund on the cashier's screen by entering their PIN | S |

### 3.16 Notifications (NTF)

Covers menu: Low Stock, Pending Payments, Salary Due, Customer Debt, Supplier Debt, Approval Requests.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-NTF-01 | In-app notifications with bell icon, unread count, list, mark read, mark all read. Each links to the related record | M |
| FR-NTF-02 | Low Stock: when a product reaches reorder level | M |
| FR-NTF-03 | Pending Payments: expenses approved but unpaid, purchase invoices due soon | M |
| FR-NTF-04 | Salary Due: days before pay day and when payroll awaits approval or payment | M |
| FR-NTF-05 | Customer Debt: invoices overdue, customer near or over credit limit | M |
| FR-NTF-06 | Supplier Debt: invoices due within the set days and overdue | M |
| FR-NTF-07 | Approval Requests: new request to approvers, decision back to requester | M |
| FR-NTF-08 | Security: account locked, login from new device, till difference above threshold, transfer difference | M |
| FR-NTF-09 | Notifications go only to users with permission to see the related record | M |
| FR-NTF-10 | Email and SMS channels through a pluggable sender | C |
| FR-NTF-11 | Daily summary to the Owner: sales, expenses, cash, low stock count, pending approvals | S |

### 3.17 Security and audit (SEC)

Covers menu: Activity Logs, Login Logs, Permission Control, Transaction Approval, Data Backup.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-SEC-01 | Activity Logs: every create, update, delete, post, void, approve, reject, export and settings change is logged with user, time, branch, IP, record and before and after values | M |
| FR-SEC-02 | Logs are searchable by user, action, record type, date. Logs cannot be edited or deleted by anyone | M |
| FR-SEC-03 | Login Logs: same data as FR-USR-08 | M |
| FR-SEC-04 | Permission Control: same screens as FR-USR-03 to FR-USR-06 | M |
| FR-SEC-05 | Transaction Approval: the approvals inbox of 3.15 filtered to money transactions | M |
| FR-SEC-06 | Data Backup: the Owner can download an export of the business's data as a file and see when the last automatic backup ran | M |
| FR-SEC-07 | Automatic full database backup every night, kept for at least 30 days, stored off the server | M |
| FR-SEC-08 | A record's own history is visible on its detail page | S |
| FR-SEC-09 | Sensitive fields (passwords, PINs, tokens) are never written to logs | M |

### 3.18 Reports (REP)

Covers menu: Sales Reports, Profit and Loss, Expense Reports, Inventory Reports, Purchase Reports, Customer Debt, Supplier Debt, Payroll Reports, Staff Performance, Branch Reports, Financial Reports. Every report takes a date range and branch filter, and exports to Excel and PDF.

| ID | Report | Content | Pri |
| --- | --- | --- | --- |
| FR-REP-01 | Sales summary | Sales, returns, discounts, tax, net sales, cost, gross profit by day, week or month | M |
| FR-REP-02 | Sales by product / category | Quantity, revenue, cost, profit, margin. Top and slow sellers | M |
| FR-REP-03 | Sales by cashier / payment method / customer | Count and value | M |
| FR-REP-04 | Till report | Per session: opening, cash sales, refunds, pay-outs, expected, counted, difference | M |
| FR-REP-05 | Profit and Loss | Income, cost of goods sold, gross profit, expenses by category, net profit. Compare with previous period | M |
| FR-REP-06 | Expense report | By category, branch, payee, month | M |
| FR-REP-07 | Inventory reports | Stock on hand, valuation, movements, low stock, dead stock, adjustments and losses | M |
| FR-REP-08 | Purchase report | By supplier, product, month. Ordered against received | M |
| FR-REP-09 | Customer Debt | Aged receivables per customer | M |
| FR-REP-10 | Supplier Debt | Aged payables per supplier | M |
| FR-REP-11 | Payroll reports | Run summary, by department, deductions summary, loans outstanding | M |
| FR-REP-12 | Staff Performance | Sales per employee, attendance rate, lateness, commission earned | S |
| FR-REP-13 | Branch Reports | Side by side branch comparison (FR-BRN-06) | M |
| FR-REP-14 | Financial: cash flow | Money in and out per account and category, opening and closing balance | M |
| FR-REP-15 | Financial: account statement | Any money account for a period with running balance | M |
| FR-REP-16 | Financial: trial balance and balance sheet | All ledger accounts with debit and credit totals; assets, liabilities, equity | S |
| FR-REP-17 | Tax report | Tax collected on sales and paid on purchases for a period | M |

### 3.19 Dashboard (DSH)

Covers menu: Total Sales, Total Expenses, Profit and Loss, Cash Balance, Stock Value, Outstanding Payments, Business Performance.

| ID | Requirement | Pri |
| --- | --- | --- |
| FR-DSH-01 | Total Sales: today, this week, this month, with change against the previous period | M |
| FR-DSH-02 | Total Expenses: same periods | M |
| FR-DSH-03 | Profit and Loss: gross and net profit for the period | M |
| FR-DSH-04 | Cash Balance: total and per money account | M |
| FR-DSH-05 | Stock Value: total inventory value at cost, count of low stock items | M |
| FR-DSH-06 | Outstanding Payments: customers owe us, we owe suppliers, salaries due | M |
| FR-DSH-07 | Business Performance: sales trend chart for 30 days, top 5 products, sales by branch, sales by payment method | M |
| FR-DSH-08 | Pending approvals and recent activity lists | M |
| FR-DSH-09 | The dashboard shows only cards the user has permission for. A cashier sees own sales today, own till status and clock-in status | M |
| FR-DSH-10 | Period and branch selectors apply to every card | M |

## 4. Non-functional requirements

| ID | Area | Requirement |
| --- | --- | --- |
| NFR-01 | Performance | POS product search returns in under 300 ms and completing a sale in under 1 s at the server, for a business with 20,000 products |
| NFR-02 | Performance | List endpoints return in under 500 ms for 95% of requests. All lists are paginated, maximum 100 rows per page |
| NFR-03 | Performance | Reports over one year of data return in under 5 s, or run as a background export |
| NFR-04 | Capacity | Version 1 supports 500 businesses, 50 concurrent users per business, 5 million sale lines in one database |
| NFR-05 | Availability | 99.5% monthly uptime target. Planned maintenance outside business hours |
| NFR-06 | Reliability | Every multi-step write is one database transaction. No partial sale, receipt, transfer or payroll can exist |
| NFR-07 | Reliability | Recovery point 24 hours or better, recovery time 4 hours or better |
| NFR-08 | Security | HTTPS only. Passwords hashed with Argon2id. Access tokens live 15 minutes, refresh tokens 30 days with rotation |
| NFR-09 | Security | Tenant isolation enforced in every query and backed by PostgreSQL row-level security |
| NFR-10 | Security | Protection against SQL injection (parameterised queries only), XSS, CSRF for cookie flows, brute force (rate limits) |
| NFR-11 | Security | Role, branch and ownership checks on the server for every endpoint |
| NFR-12 | Auditability | Every state-changing request leaves an audit record. Financial history is immutable |
| NFR-13 | Usability | A cashier completes a three-item cash sale in under 30 seconds using keyboard and scanner only |
| NFR-14 | Usability | Responsive from 360 px wide. English and Kiswahili. Clear error messages that say what to do |
| NFR-15 | Accuracy | Money uses exact decimals. Rounding rule is defined once: round half up to the currency's decimal places, per line |
| NFR-16 | Maintainability | Modular code with one folder per module, database migrations in version control, automated tests for all money and stock workflows, at least 70% coverage on service code |
| NFR-17 | Observability | Structured logs with request ID, health endpoint, error tracking, slow query log |
| NFR-18 | Portability | Runs from Docker images on any Linux host. Configuration only through environment variables |
| NFR-19 | Data | Timestamps stored in UTC and shown in the business timezone. A business can export all its data |
| NFR-20 | Compatibility | Works with keyboard-emulating barcode scanners and browser printing to thermal and A4 printers |

## 5. External interfaces

- **User interface.** A single-page web app with three layouts: admin layout with sidebar, full-screen POS layout, and a simple self-service layout for staff.
- **Hardware.** Barcode scanner as keyboard input. Receipt printer through the browser print dialog with a print stylesheet. Cash drawer opened by the printer, if wired that way.
- **Software.** REST JSON API under `/api/v1`. File storage through an S3-compatible interface or local disk. Optional SMS or email gateway behind a sender interface. Optional fiscal receipt service behind an interface.
- **Communication.** HTTPS with TLS 1.2 or newer. JSON in UTF-8.

## 6. Business rules

| ID | Rule |
| --- | --- |
| BR-01 | All data belongs to one business. No user can see or change another business's data |
| BR-02 | Posted documents are never edited or deleted. Corrections are void, return, reversal or adjustment |
| BR-03 | Stock on hand equals the sum of stock movements for that product and branch |
| BR-04 | Every journal entry balances: total debit equals total credit |
| BR-05 | Inventory cost uses weighted average per product per branch. New average = (old qty x old cost + received qty x received cost) / (old qty + received qty) |
| BR-06 | Sale line cost is the average cost at the moment of sale and never changes afterwards |
| BR-07 | Line total = quantity x unit price - line discount. Tax is calculated per line on the discounted amount, then summed |
| BR-08 | A credit sale needs a named customer and must keep balance within the credit limit unless approved |
| BR-09 | Payments are allocated oldest invoice first unless the user chooses otherwise |
| BR-10 | Returned quantity cannot exceed sold quantity minus quantity already returned |
| BR-11 | A user cannot approve their own request, except the Owner |
| BR-12 | Approval limits: the action goes through if amount is within the actor's limit, else it waits for a role whose limit covers it |
| BR-13 | Document numbers are unique per business, sequential per branch and type, and never reused |
| BR-14 | No posting into a locked period |
| BR-15 | A sale belongs to the business day in which it was completed, using the business timezone and day start time |
| BR-16 | Gross pay = prorated base + allowances + overtime + commission + bonuses. Net pay = gross - deductions - loan repayment. Net pay cannot be negative |
| BR-17 | Daily rate = monthly base / working days per month. Hourly rate = daily rate / standard hours per day |
| BR-18 | Overtime counts for pay only when approved |
| BR-19 | Goods in transit between branches belong to neither branch's sellable stock |
| BR-20 | At least one active Owner must always exist |

## 7. Acceptance criteria

A module is accepted when its test passes on a clean database with seed data.

| Module | Acceptance test |
| --- | --- |
| Auth and users | Register a business, log in as Owner, create a Manager and a Cashier in branch A. Cashier gets 403 on products create, Manager gets 404 on a branch B sale |
| Settings | Change tax rate and receipt footer. The next sale uses both, the previous sale is unchanged |
| Inventory | Stock in 10 at 1,000 and 10 at 2,000. Average cost shows 1,500, quantity 20, valuation 30,000. Stock card shows two movements |
| Sales | Sell 3 of that product for cash at 2,500. Stock is 17, cash up 7,500, profit 3,000. Send the same request again with the same idempotency key: still one sale |
| Returns | Return 1 unit. Stock is 18, cash down 2,500. Try to return 3 more: rejected |
| Customers | Credit sale of 50,000 to a customer with limit 40,000: blocked. With limit 60,000: allowed, balance 50,000. Pay 20,000: balance 30,000, statement shows both |
| Purchases | PO for 100 units, receive 60, then 40. PO status moves to partially received then received. Invoice creates supplier debt. Payment clears it |
| Transfers | Send 10 from A to B, receive 9. A is down 10, B is up 9, 1 recorded as transfer loss, Owner notified |
| Expenses | Cashier submits 100,000 above limit: pending. Manager approves and pays from petty cash: petty cash down, expense in profit and loss |
| Finance | Transfer 500,000 from till to bank. Both balances change, transactions history shows both sides, trial balance still balances |
| Attendance | Clock in 20 minutes after start with grace 10: marked late 20 minutes. Missing clock-out is flagged |
| Payroll | Run payroll for an employee with base 600,000, allowance 50,000, 2 unpaid days of 26, loan repayment 50,000. Payslip matches hand calculation. Approve and pay: cash down by net, loan balance down 50,000, run locked |
| Approvals | A user cannot approve their own request. Rejecting returns the record to its previous state |
| Reports | Profit and loss for the test month equals sales minus cost minus expenses entered in the tests above |
| Audit | Every step above appears in the audit log with the correct user |
| Tenancy | A second business sees none of the data above through any endpoint |
