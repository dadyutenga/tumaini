# **Website Design and Screen Specification** 

Nov 15, 2026 · @tumaini 

## **1. Overview and design principles** 

This document describes every screen of the website: what the user sees, what they can click, and what the system does. A developer or designer should be able to build the website from it without further questions. It builds on the Business Management System Specification (modules, roles and data). 

- **Users:** the Owner, the Secretary or Manager, and Staff or Cashiers. What each one sees depends on their role. 

- **Devices:** works on a computer, tablet and phone browser. The secretary mainly uses a computer; the POS also works on a tablet. 

- **Look and feel:** clean and simple, white background, one main brand color for buttons and highlights, large readable text, and the business logo at the top left. 

- **Language:** Swahili and English, switchable from the top bar. 

- **Speed first:** the most common actions (new sale, new client, receive payment) are one click from anywhere. 

- **Few mistakes:** required fields are marked, amounts are checked, and risky actions (delete, refund, price change) ask for confirmation and approval. 

- **Everything is traceable:** every record shows who created it and when. 

## **2. Site map** 

The left sidebar shows these menus. Each menu opens a list page, and each item in a list opens a detail page. Menus a role cannot use are hidden. 

1. **Dashboard** 

2. **Shop (Business A)** 

   - POS / New Sale 

   - Sales History, Returns 

   - Products, Categories 

   - Stock (in, out, adjustments, transfers) 

   - Shop Customers 

   - Suppliers, Purchases 

3. **Services (Business B)** 

   - Clients and Leads 

   - Projects 

   - Service Catalog 

   - Quotations, Invoices 

   - Documents 

   - Tasks and Calendar 

   - Renewals 

   - Design Jobs (graphic design work) 

   - Contractors 

4. **Finance** 

   - Accounts (cash, bank, mobile money) 

   - Transactions 

   - Payments Received, Payments Made 

   - Expenses, Petty Cash 

   - Debts (customers and suppliers) 

   - End-of-Day Closing 

5. **Staff** 

   - Employees, Attendance, Leave 

   - Advances and Loans 

   - Payroll, Payslips 

6. **Reports** 

7. **Notifications** 

8. **Users and Security** 

   - Users, Roles and Permissions 

- Audit Log, Login History, Backup 

- 9. **Settings** 

Pages outside the menu: Login, Forgot Password, My Profile. 

## **2A. Roles: what Admin, Manager and Staff see** 

The system has three roles. Wherever this document says "Owner", it means Admin, and "Secretary" means Manager. Customers and clients do not log in; they only receive receipts, invoices and approval links. 

- **Admin:** the business owner. Sees and controls everything, including money, profit, salaries, settings and security. 

- **Manager:** runs daily operations. Sees the whole business except the most sensitive financial and system areas. 

- **Staff (member):** the person who sells and enters jobs. Sees only what is needed to sell products, register clients, enter jobs and track their own work and pay. 

The Admin can change any permission for any individual user in **Users and Security > Roles and Permissions** . The tables below are the defaults. 

### **Menu visibility** 

|Menu|Admin|Manager|Staff|
|---|---|---|---|
|Dashboard|Full<br>business<br>dashboard|Operations<br>dashboard|Personal dashboard (own sales,<br>jobs, tasks)|
|Shop: POS / New Sale|Yes|Yes|Yes|



|Menu|Admin|Manager|Staff|
|---|---|---|---|
|Shop: Sales History|All sales|All sales|Own sales only|
|Shop: Products|Full,<br>including<br>buying<br>price|Full, including<br>buying price|View only: photo, name, selling<br>price, stock|
|Shop: Stock|Full|Stock in, out,<br>adjustments|Stock level on products only|
|Shop: Customers|Full|Full|Add and view customers they<br>serve|
|Shop: Suppliers, Purchases|Full|Create and manage|Hidden|
|Services: Clients, Projects|All|All, can assign|Own jobs and assigned jobs|
|Services: Quotations,<br>Invoices|Full|Create, edit, send|Create drafts for own jobs|
|Services: Design Jobs|All|All, can assign|Assigned jobs; create briefs|
|Services: Documents, Tasks|All|All|Own clients and own tasks|
|Services: Renewals,<br>Contractors|Full|Full|View renewals of own clients;<br>contractors hidden|
|Finance: Accounts,<br>Transactions|Full|Limited view|Hidden|
|Finance: Payments Received|Full|Record and view all|Record and view own|
|Finance: Expenses|Approve all|Enter; approve up to<br>a limit|Submit requests only|
|Finance: Debts, End-of-Day<br>Closing|Full|Full|Own sales summary only|
|Staff: Employees, Payroll|Full|Attendance and<br>leave only|Own attendance and own<br>payslips|
|Reports|All reports|Sales, stock,<br>projects, debts|Own sales and jobs|
|Notifications|All types|Operations types|Own tasks, jobs and approvals|
|Users and Security|Full|Hidden|Hidden|
|Settings|Full|Hidden|Hidden|



|Menu|Admin|Manager|Staff|
|---|---|---|---|



### **Admin: what the Admin sees and can do** 

- **Sees:** every screen, every figure, including profit, buying prices, bank and mobile money balances, all salaries, the audit log and login history. 

- **Can:** create and deactivate users, set roles and permissions, change prices and discount limits, approve expenses, refunds and large discounts, void transactions, run payroll, reopen a closed day, change settings, download backups and restore data. 

- **Dashboard:** sales, income, expenses, profit, cash balances, debts, projects by stage, overdue tasks, low stock, renewals due, and approvals waiting. 

### **Manager: what the Manager sees and can do** 

- **Sees:** sales, products with buying prices, stock, customers, clients, projects, design jobs, invoices, debts, expenses, attendance and operational reports. Net profit, bank balances, salaries and settings are hidden by default; the Admin can turn them on. 

- **Can:** register clients and projects, assign jobs and tasks to staff, issue quotations, invoices and receipts, receive payments, enter expenses and approve those under the limit set by the Admin, approve refunds and discounts under the limit, do stock in, stock out and adjustments, create purchases, close the day, approve leave, and correct attendance with a reason. 

- **Cannot:** see other staff's salaries, change settings, manage users and permissions, view or delete the audit log, or restore backups. 

- **Dashboard:** today's and this month's sales, projects by stage, overdue tasks, low stock, renewals due, debts, and approvals waiting. 

### **Staff: what the Staff member sees and can do** 

Staff are the people at the counter and on client jobs. Their screens are simple and built for speed. 

- **Sees:** the product catalog with photo, name, selling price and stock level; their own sales, commission, jobs, tasks, attendance and payslips; and the clients they registered or were assigned. 

- **Can:** choose products and make sales, apply a discount up to their limit, receive payments, print or send receipts, register customers and clients, enter service jobs and design briefs, upload client documents, update the status of their own jobs and tasks, clock in and out, submit expense requests, and request refunds or larger discounts. 

- **Cannot see:** buying prices, profit, suppliers, other staff's sales or pay, company totals, bank and cash accounts, expenses, payroll, reports on the business, the audit log, users and settings. 

- **Cannot do:** edit or delete a completed sale, change a product price, give a discount above their limit without approval, refund money without approval, or delete any record. 

### **Staff home screen** 

+----------------------------------------------------------+ | LOGO  [ Search products or clients... ]  [Alerts] [User v]| +----------------------------------------------------------+ | Good morning, Name                          [Clock In]   | +----------------------------------------------------------+ | [   NEW SALE   ]  [   NEW JOB   ]  [ RECEIVE PAYMENT ]  | +-------------------------+--------------------------------+ | My sales today          | My jobs                        | | Sales: --  Total: ---   | Job       Client    Due  Status| | My commission: ---      | Logo      Client A  3d   Design| |                         | TIN       Client B  5d   Open  | +-------------------------+--------------------------------+ | My tasks                | Products running low           | | [ ] Call Client A       | Charger (2 left)               | | [ ] Upload ID for B     | Earbuds (3 left)               | +-------------------------+--------------------------------+ 

The staff sidebar is short: Home, New Sale, Products, My Sales, My Jobs, Clients, Tasks, My Attendance, My Payslips, Notifications. 

### **How Staff choose products and sell** 

The Products page for Staff is a catalog, not a management screen. 

+----------------------------------------------------------+ | Products   [ Search... ]  [Category v]  [In stock only]  | +----------------------------------------------------------+ | [photo] Phone A      TZS ---    In stock: 12     [Sell]  | | [photo] Charger      TZS ---    In stock: 40     [Sell]  | | [photo] Earbuds      TZS ---    Low: 2           [Sell]  | +----------------------------------------------------------+ 

1. The staff member searches, scans a barcode, or filters by category and taps **Sell** on a product. It is added to the sale. 

2. In **New Sale** they change quantity, enter the serial number or IMEI for electronics, choose the customer, and choose the payment method. 

3. The price is fixed. A discount up to the staff member's limit applies immediately; above the limit it is sent to the Manager for approval and the sale waits. 

4. **Complete** saves the sale, reduces stock, posts to Finance, and records the sale under the staff member's name so commission is calculated. 

5. The receipt is printed or sent by WhatsApp. The sale then appears in **My Sales** as read only. 

### **How Staff enter jobs** 

The **New Job** button opens a short form so staff can register service work quickly. 

|Field|Notes|
|---|---|
|Client|Pick or add a client (name and phone are enough to start)|
|Job type|Business service (company name, TIN, license and similar), design job, or other,<br>chosen from the Service Catalog|
|Details|What the client wants|
|Price|Filled from the catalog; staff cannot lower it, and can request a change that the<br>Manager approves|
|Deposit|Amount paid now; a receipt is issued|
|Documents|Photograph or upload the client's documents|
|Deadline|Suggested from the catalog, editable|



The job then appears in **My Jobs** and on the Manager's board. The staff member can update the status of their own jobs, add notes, upload documents and send updates to the client. The Manager assigns or reassigns the job if another person should handle it. 

### **Approval ladder** 

|Request|Raised by|Approved by|
|---|---|---|
|Discount above the staff limit|Staff|Manager|
|Refund or return|Staff|Manager (Admin above the Manager's<br>limit)|
|Price change on a job|Staff|Manager|
|Expense|Staff or Manager|Manager up to the limit; Admin above it|
|Void a sale or transaction|Manager|Admin|
|Reopen a closed day|Manager|Admin|



Every approval, rejection and sensitive action is written to the audit log with the person's name and the time. 

## **3. Global layout** 

Every page after login uses the same frame: a top bar, a left sidebar and a main work area. 

+----------------------------------------------------------+ | LOGO  [ Search clients, products, invoices... ]  [+ New] | |                      [Alerts 3] [Sw/En] [User v]         | +-------------+--------------------------------------------+ 

| Dashboard   |  Page title                  [Main button] | 

|| Shop        |  ----------------------------------------- ||
|---|
|| Services    |  [Date] [Status] [Business] [Search...]    |<br>| Finance     |                                            |<br>f|
|| Staff       |  +--------------------------------------+  ||
|| Reports     |  |  Table, cards or form                |  ||
|| Alerts      |  |                                      |  ||
|| Users       |  +--------------------------------------+  ||
|| Settings    |  Page 1 2 3 ...                [Export]    |<br>+++|
|---------------------------------------------------------<br>Part<br>What it contains|
|Logo<br>The business logo; clicking it goes to the Dashboard|
|Global search<br>One box that finds clients, products, invoices, projects and receipts by<br>name, phone, number or barcode|
|"+ New" button<br>Quick menu: New Sale, New Client, New Project, Receive Payment,<br>New Expense, New Task|
|Alerts bell<br>Number of unread notifications; opens a list of the latest ones|
|Language switch<br>Swahili or English|
|User menu<br>My Profile, Change Password, Sign Out; shows the user's name and role|
|Sidebar<br>The menus from the site map; the current page is highlighted; groups<br>expand and collapse|
|Page header<br>Page title, breadcrumb (for example Services > Projects > Mama Amina<br>Shop), and the page's main button|
|Filters row<br>Date range, status, business (A or B), and a search box where they<br>apply|
|Work area<br>Tables, forms, cards or charts for the current page|



On a phone, the sidebar becomes a menu button at the top left, and the "+ New" button stays visible at the bottom right. 

## **4. Login and account screens** 

|Screen|What the user sees and can do|
|---|---|
|Login|Business logo, fields for phone or email and password, a "Show<br>i    t|
||password" toggle, a "Remember me" tick, a Sign In button, and a<br>"Forgot password?" link. After 5 wrong attempts the account locks for<br>ii|
||15 minutes and the Owner is notified.|
|Forgot password|Enter phone or email; the system sends a reset code by SMS or email;|



|Screen|What the user sees and can do|
|---|---|
||the user enters the code and a new password.|
|First login|A new user must change the temporary password set by the Owner<br>before continuing.|
|My Profile|Name, phone, email, photo, role (read only), language. Buttons: Save,<br>Change Password. Also a list of the user's recent logins.|
|Session|The user is signed out automatically after 30 minutes of no activity, with<br>a warning one minute before. A POS sale in progress is saved as a draft.|
|No access|If a user opens a page their role cannot use, they see a "You do not<br>have permission" message with a button back to the Dashboard.|



## **5. Dashboard** 

The first page after login. The Owner sees everything below; the Secretary sees the same page without profit, salary and bank figures; Staff see only their own tasks and today's sales. 

|+--------------------------------------------<br>| Dashboard          [This month<br>+------------+------------+------------+--<br>i|--------------+<br>v]  [Both businesses v]   |<br>-----------------+<br>i||
|---|---|---|
|| Shop sales | Service    | Exp    i|enses   | Profit            |||
|| TZS ---    | income     | TZS ---<br>|            | TZS ---    |            |<br>+------------+------------+------------+--<br>| Income vs expenses by day<br>| (line chart)                | Cash:|| TZS ---           |<br>|<br>-----------------+<br>| Cash balances              |<br>TZS ---       |||
||                             | Bank:        T<br>|                             | Mobile mo<br>+-----------------------------+-------------<br>| Projects by stage           | Deb<br>| (bar per stage)             | Owe<br>+-----------------------------+-------------|ZS ---       |<br>ney: TZS ---      |<br>---------------+<br>ts                      |<br>d to us / We owe        |<br>---------------+||
|| Overdue tasks | Low stock<br>+---------------+--------------+-----------|| Renewals due | Approvals   |<br>---+-------------+||
|Widget|Shows|Clicking it opens|
|Period and business filters|Today, this week, this month, custom dates;<br>Business A, B or both|Updates every<br>widget on the<br>page|
|Four total cards|Shop sales, service income, expenses, profit for<br>the chosen period, with the change against the<br>previous period|The matching<br>report|
|Income vs expenses|A line chart byday|Transactions list|



|Widget|Shows|Clicking it opens|
|---|---|---|
|||for that day|
|Cash balances|Balance of cash, each bank account and each<br>mobile money account|Accounts page|
|Projects by stage|How many projects are in each stage, and how<br>many are stuck or late|Projects list<br>filtered by that<br>stage|
|Debts|Total owed by customers and clients, and total<br>owed to suppliers and contractors|Debts page|
|Overdue tasks|Tasks past their deadline, with the person<br>responsible|Tasks list|
|Low stock|Products at or below the minimum level|Products list<br>filtered to low<br>stock|
|Renewals due|Licenses and fees due in the next 30 days|Renewals list|
|Approvals|Expenses, refunds and discounts waiting for<br>approval, with Approve and Reject buttons|The request itself|



## **6. Business A screens: Shop** 

### **POS / New Sale** 

The busiest screen. Everything needed for a sale is on one page, with no scrolling on a computer or tablet. 

+----------------------------------------------------------+ | New Sale   Receipt #0001      [Hold]  [Held sales]       | +--------------------------------+-------------------------+ | [ Scan or search product... ]  | Customer: [Walk-in v][+]| |                                +-------------------------+ | [Phone A]  [Charger] [Cable]   | Item     Qty  Price Total| | [Earbuds]  [Speaker] [Fan]     | Phone A   1   ---   ---  | | [Torch]    [Battery] [Adapter] | Charger   2   ---   ---  | |                                +-------------------------+ |                                | Subtotal           ---  | |                                | Discount  [   ]    ---  | |                                | TOTAL              ---  | |                                | [Cash] [M-Pesa] [Bank]  | |                                | [Credit]                | |                                | [Cancel]     [Complete] | +--------------------------------+-------------------------+ 

- Scan a barcode, type a name, or tap a product tile to add it. Tapping again raises the quantity. 

- • Each line shows the product, quantity, price and total, with buttons to change quantity or remove the line. For electronics the cashier enters or scans the serial number or IMEI. 

- Customer defaults to Walk-in. Selecting a registered customer is required for credit sales and shows their current debt. 

- Discount can be an amount or a percent. Above the limit set by the Owner, the sale needs Manager approval. 

- Payment can be split, for example part cash and part M-Pesa. Credit sales ask for the amount paid now and the due date. 

- Complete saves the sale, reduces stock, posts to Finance, and opens the receipt with Print and Send by WhatsApp or SMS buttons. 

- Hold saves the sale for later so another customer can be served; held sales appear under Held sales. 

- The stock level is shown on each product tile, and a product with zero stock cannot be added unless the Owner allows it. 

### **Other shop screens** 

|Screen|What it shows|Main actions and fields|
|---|---|---|
|Sales History|Table of sales: receipt number,<br>date, customer, items, total,<br>paid, balance, payment method,<br>cashier, status|Filter by date, cashier, method, status; open<br>a sale; reprint receipt; start a return; export|
|Returns|Table of returns with receipt,<br>product, reason, refund amount,<br>approval status|New return (pick the sale, the items, reason,<br>refund or exchange); Manager approves|
|Products|Table: photo, name, category,<br>stock, buying price, selling price,<br>status|Add Product form: name, category, barcode,<br>buying price, selling price, opening stock,<br>minimum level, warranty period, photo,<br>whether it has serial numbers. Edit,<br>deactivate, import from Excel, export|
|Product detail|Product information, stock<br>history, sales history, profit per<br>unit|Edit, adjust stock, print barcode label|
|Categories|Simple list of categories|Add, rename, deactivate|
|Stock|Current stock per product and<br>total stock value|Stock In, Stock Out, Adjustment (reason:<br>damaged, lost, found), stock count sheet;<br>every movement shows who did it|
|Shop Customers|Table: name, phone, total<br>bought, current debt|Add customer; open customer; record debt<br>payment|



|Screen|What it shows|Main actions and fields|
|---|---|---|
|Customer detail|Profile, purchase history, debts,<br>payments, statement|Receive payment, print or send statement|
|Suppliers|Table: name, phone, goods<br>supplied, amount we owe|Add supplier; pay supplier; statement|
|Purchases|Table: purchase number,<br>supplier, date, total, paid,<br>balance, status|New Purchase: pick supplier, add products<br>with quantity and cost, record goods<br>received and payment; stock increases when<br>received|



## **7. Business B screens: Services** 

### **Project detail** 

The most important screen for the secretary. Everything about one client's job is here. 

+----------------------------------------------------------+ | Project: Client Business Name      Status: In progress   | | Client: Client Name   Service: Full startup package      | | [Edit] [Add Payment] [Add Expense] [Send Update to Client]| +----------------------------------------------------------+ | Progress [#####-----]  5 of 10 stages                    | +----------------------------------------------------------+ | Stage            | Status      | Owner   | Deadline      | | 1 Client intake  | Done        | Sec.    | 02 Dec        | | 2 Name reg.      | Done        | Sec.    | 05 Dec        | | 3 TIN            | In progress | Staff 1 | 10 Dec        | | 4 License        | Not started | Staff 1 | 15 Dec        | +----------------------------------------------------------+ | [Overview] [Documents] [Payments] [Costs] [Notes]        | +----------------------------------------------------------+ | Total: ---   Paid: ---   Balance: ---   Costs: ---        | +----------------------------------------------------------+ 

- **Stage table:** each stage has a status (not started, in progress, done, stuck), an owner, a deadline and a checklist. Changing a status asks for an optional note and records who changed it and when. 

- **Documents tab:** the required documents for each stage, each marked Received or Missing, with Upload and Download buttons. 

- **Payments tab:** deposit and installments, each with its receipt; a button to add a payment; the balance still due. 

- **Costs tab:** government fees, contractors and materials spent on this project; the project's profit is shown as payments received minus costs. 

- **Notes tab:** calls, messages and meetings with the client, with dates. 

- **Send Update to Client:** sends an SMS or WhatsApp message such as "Your TIN is ready", using a ready template that the secretary can edit. 

### **Other services screens** 

|Screen|What it shows|Main actions and fields|
|---|---|---|
|Clients and Leads|Table: name, phone, status<br>(lead, active, completed),<br>source, number of projects,<br>balance owed|Add Client form: name, phone, email,<br>address, ID number, source, what they need,<br>notes. Convert a lead to a client; filter and<br>search; export|
|Client detail|Profile, projects, invoices,<br>payments, documents,<br>communication history|Edit, start a new project, create quotation,<br>receive payment, print statement|
|Projects|Two views: a table and a<br>board with one column per<br>stage|New Project: pick client, pick service or<br>package, set start date, owner and total<br>price. Stages and required documents are<br>created automatically from the service|
|Service Catalog|List of services with price<br>and number of stages|Add or edit a service: name, description,<br>standard price, stages in order, required<br>documents, estimated days per stage,<br>government fees. Group services into<br>packages|
|Quotations|Table: number, client, total,<br>date, status (draft, sent,<br>accepted, rejected)|New Quotation: pick client, add services,<br>discount, validity date; print, send, and<br>convert to an invoice and a project when<br>accepted|
|Invoices|Table: number, client,<br>project, total, paid, balance,<br>due date, status|Create from a quotation or project; record<br>payments against it; print and send; deposit<br>and installment plan|
|Documents|Folders by client and<br>project; each file shows<br>type, date and who<br>uploaded it|Upload, preview, download, mark as<br>received; search by client or document type|
|Tasks and Calendar|List and calendar of tasks<br>with owner, project,<br>deadline, status|New Task, assign, change status; filter by<br>person, project, date; overdue tasks in red|
|Renewals|Table: client, item (license,<br>annual fee), due date, days<br>left, status|Add renewal; mark as renewed; send<br>reminder to the client; reminders go out<br>automatically at 30 and 7 days|



|Screen|What it shows|Main actions and fields|
|---|---|---|
|Contractors|Table: name, trade (printing,<br>painting, carpentry), phone,<br>amount we owe|Add contractor; record work and payments;<br>statement|



## **7A. Design Jobs (graphic designer work)** 

Design Jobs track creative work such as logos, business cards, posters, flyers, banners, signage, social media graphics and full branding packages. A design job can stand alone for a walk-in client, or belong to a startup project, for example the Branding stage. It sits under **Services > Design Jobs** in the sidebar. Designers use a Staff login with design permissions and see only the jobs assigned to them; the Secretary and Owner see all jobs. 

### **Design Jobs board** 

The default view is a board with one column per step, so the whole team sees what is being designed and what is waiting. A List view is available for searching and filtering. 

+----------------------------------------------------------+ | Design Jobs   [Board|List]  [Designer v]  [+ New Job]    | +---------+---------+---------+---------+------------------+ | New     | In      | Internal| With    | Approved         | | Brief   | Design  | Review  | Client  |                  | +---------+---------+---------+---------+------------------+ | [Logo   | [Poster | [Flyer  | [Cards  | [Banner          | |  Client | Client  | Client  | Client  |  Client          | |  A]     | B]      | C]      | D]      |  E]              | | Due 3d  | Due 1d  | Due 2d  | Due 5d  | Ready to print   | +---------+---------+---------+---------+------------------+ 

Each card shows the job title, client, designer, due date (red if late) and revision count. Dragging a card to the next column changes its status. Further columns after Approved: Printing and Delivered. 

### **New Design Job form** 

|Field|Notes|
|---|---|
|Client|Pick an existing client or add one|
|Project (optional)|Links the job to a startup project; its cost and income then count<br>toward that project|
|Job type|Logo, business card, poster, flyer, banner, signage, social media<br>post, packaging label, full branding package, other|
|Title|For example "Logo for Client Business"|
|Brief|What the client wants, in their own words|



|Field|Notes|
|---|---|
|Size and format|A4, A3, business card, custom size, banner size; print or digital|
|Style notes|Preferred colors, mood, fonts, examples the client likes|
|Text to include|Business name, slogan, phone, address, social handles|
|Reference files|Upload logos, photos and examples from the client|
|Deadline and priority|Normal, urgent|
|Designer|Who is assigned|
|Price and deposit|Taken from the Service Catalog, editable by the Secretary|
|Revisions included|For example 3; extra revisions can be charged|
|Printing needed|Yes or no; if yes, a print order is created after approval|



### **Design Job detail** 

+----------------------------------------------------------+ 

| Design Job: Logo for Client Business   Status: In Design | | Client: Client Name   Project: Full startup package      | | Designer: Staff 1   Due: 12 Dec   Revisions: 1 of 3      | | [Edit] [Upload Version] [Send for Approval] [Add Payment]| 

+----------------------------------------------------------+ | [Brief] [Files] [Feedback] [Costs] [Payments] [History]  | +----------------------------------------------------------+ | Version 2 (current)  uploaded 10 Dec by Staff 1          | | [ preview ]          Status: Sent to client              | | Version 1            Status: Changes requested           | | [ preview ]          "Make the text bigger"              | +----------------------------------------------------------+ | Price: ---   Paid: ---   Costs: ---   Profit: ---        | +----------------------------------------------------------+ 

- **Brief tab:** everything entered on the form, plus the reference files. 

- **Files tab:** every version the designer uploads (image or PDF, or a link to the working file), each with a preview, date and status: Draft, Sent to client, Changes requested, Approved. The approved version is marked **Final** , and the designer adds the print-ready PDF, PNG and a link to the source file. 

- **Feedback tab:** the client's comments on each version, and the designer's replies. 

- **Costs tab:** designer time (optional), printing, stock images, fonts and contractors; profit is payments received minus costs. 

- **Payments tab:** deposit and balance, with receipts, the same as for other projects. 

- **Revision counter:** each "Changes requested" uses one revision. When the included revisions are used up, the job shows a warning and the Secretary can add an extra charge. 

### **Client approval page** 

The designer or Secretary clicks **Send for Approval** . The client receives an SMS or WhatsApp message with a private link, and does not need to log in. 

+--------------------------------------------+ | LOGO   Design for approval: Logo           | |                                            | |   [ large preview of the design ]          | |   Version 2                                | |                                            | | Comments: [ type here...                 ] | | [Approve]            [Request Changes]     | +--------------------------------------------+ 

The link shows only that design, works for 14 days, and records the time and the client's choice. Approve marks the version Final and notifies the designer and Secretary. Request Changes sends the comment to the designer and moves the card back to In Design. 

### **Other design features** 

|Feature|What it does|
|---|---|
|My Jobs (designer's dashboard)|Jobs assigned to the designer, due today, waiting for client<br>feedback, and changes requested; a Start and Stop timer<br>to log hours|
|Print orders|After approval, create a print order for a contractor<br>(printer): quantity, cost, due date, status (sent to print,<br>ready, delivered). The cost is added to the job|
|Design price list|In the Service Catalog, a Design category lists standard<br>prices and included revisions (for example logo, business<br>card, poster, branding package)|
|Branding stage link|A startup project's Branding stage can create several<br>design jobs at once (logo, signage, business cards); the<br>stage is marked Done when all linked jobs are Approved or<br>Delivered|
|File library|Final files stay in the client's Documents folder so the<br>client can get them again later|
|Notifications|New brief assigned, client feedback received, deadline in 2<br>days, job approved, print ready|
|Reports|Jobs by designer, on-time rate, average days to approval,<br>revisions per job, design income and profit per job|



|Status|Color|
|---|---|
|New Brief, Draft|Grey|
|In Design, Internal Review|Yellow|
|With Client|Blue|
|Changes Requested, Overdue|Red|
|Approved, Printing, Delivered|Green|



New data needed: design_jobs (client, project, type, brief, size, deadline, designer, price, revisions included, status), design_versions (job, version number, file, status, uploaded by, date) and design_feedback (job, version, comment, author, date). 

## **8. Finance and expenses screens** 

All money movements appear in Finance. Staff see only the screens their role allows. 

|Screen|What it shows|Main actions and fields|
|---|---|---|
|Accounts|Cards for cash, each bank account<br>and each mobile money account,<br>with the balance of each|Add account; transfer between<br>accounts (from, to, amount, note);<br>open an account to see its transactions|
|Transactions|Table: date, account, type (income<br>or expense), amount, business (A<br>or B), project, reference (sale,<br>payment, salary), user|Filter by date, account, type, business,<br>project; export. Transactions cannot be<br>deleted; a mistake is fixed with a<br>correction entry that needs approval|
|Payments Received|Table of money received from<br>customers and clients, with the<br>invoice or sale it belongs to|Receive Payment form: pick customer<br>or client, pick invoice or debt, amount,<br>method, reference number, date; the<br>receipt is created automatically|
|Payments Made|Table of payments to suppliers,<br>contractors and others|Make Payment form: pick payee, pick<br>purchase or bill, amount, account, date|
|Debts|Two tabs: Owed to us (customers<br>and clients) and We owe<br>(suppliers and contractors), each<br>with age of the debt: 0 to 30, 31<br>to 60, over 60 days|Receive or make a payment; send a<br>reminder by SMS or WhatsApp|
|Expenses|Table: date, category, description,<br>amount, business, project, paid<br>from, receipt photo, status<br>(pending, approved, rejected)|New Expense form: category, amount,<br>date, business, project (optional),<br>account, receipt photo. Manager or<br>Owner approves; recurring expenses|



|Screen|What it shows|Main actions and fields|
|---|---|---|
|||can be set monthly|
|Petty Cash|Small daily cash with opening<br>balance, expenses and closing<br>balance|Add petty cash expense; top up;<br>reconcile|
|End-of-Day Closing|For one day: sales by payment<br>method, payments received,<br>expenses, expected cash|The user enters the counted cash; the<br>screen shows the difference. Close Day<br>locks that day's entries; only the<br>Owner can reopen it|



## **9. Staff, attendance and payroll screens** 

Only the Owner sees salaries and payroll. A staff member sees only their own attendance and payslips. 

|Screen|What it shows|Main actions and fields|
|---|---|---|
|Employees|Table: photo, name,<br>position, phone, salary,<br>status|Add Employee form: name, phone, ID<br>number, position, start date, basic<br>salary, allowances, payment account,<br>linked user login. Deactivate instead<br>of delete|
|Employee detail|Profile, attendance, leave,<br>advances, commissions,<br>payslips|Edit, record a bonus or deduction,<br>print statement|
|Attendance|Today's list with clock-in<br>and clock-out times, late<br>and absent marks; monthly<br>view per employee|Clock In and Clock Out buttons on the<br>staff member's own dashboard; the<br>secretary can correct a record with a<br>reason that is logged|
|Leave|Table of leave requests<br>with dates, type and status|Request leave; Manager approves or<br>rejects|
|Advances and Loans|Table: employee, amount,<br>date, amount repaid,<br>balance|New advance; repayment is deducted<br>from salary automatically each month|
|Commissions and Bonuses|Table of amounts per<br>employee and reason,<br>linked to sales or projects|Add commission rule (percent of sale<br>or fixed amount per completed<br>project) or a one-time bonus|
|Payroll|Month selector, then a<br>table per employee: basic<br>salary, allowances,<br>commission,overtime,|Generate Payroll, review and edit<br>lines, Approve, then Pay: choose the<br>account and the system records each|



|Screen|What it shows|Main actions and fields|
|---|---|---|
||deductions, advance<br>repayment, net pay|salary payment in Finance|
|Payslips|One payslip per employee<br>per month|Print, download PDF, send by<br>WhatsApp or email; staff see only<br>their own|



## **10. Reports, notifications, users and settings screens** 

|Screen|What it shows|Main actions and fields|
|---|---|---|
|Reports home|Cards grouped as Sales, Profit and Loss,<br>Projects, Expenses, Stock, Debts, Staff,<br>Cash Flow|Click a card to open the<br>report|
|Any report|Filters at the top (date range, business,<br>person, category), a summary row of<br>totals, a chart, and a detailed table|Change filters, Export<br>to PDF, Export to Excel,<br>Print|
|Profit and Loss|Income, cost of goods, expenses and net<br>profit for Business A, Business B and the<br>total|Choose period;<br>compare with the<br>previous period|
|Project Profit|One row per project: price, payments<br>received, costs, profit|Click a row to open the<br>project|
|Notifications|List of alerts (low stock, debt due, task<br>overdue, renewal due, salary due,<br>approval waiting), newest first, unread in<br>bold|Open the related<br>record; Mark as read;<br>Mark all as read|
|Users|Table: name, role, phone, last login, status|Add User: name,<br>phone, email, role,<br>temporary password.<br>Deactivate, reset<br>password, force sign<br>out|
|Roles and Permissions|A grid with modules as rows and actions<br>as columns (view, add, edit, delete,<br>approve, export)|Tick boxes per role;<br>create a custom role|
|Audit Log|Table: time, user, action, record, old<br>value, new value|Filter by user, date,<br>module; Export. Read<br>only; nobody can edit<br>or delete entries|



|Screen|What it shows|Main actions and fields|
|---|---|---|
|Login History|Table: user, time, device, location, success<br>or failure|Filter; Export|
|Backup|List of automatic daily backups with date<br>and size|Download a backup;<br>Restore (Owner only,<br>asks for password);<br>Run Backup Now|
|Settings: Business Profile|Business name, logo, address, phone,<br>email, TIN, registration details|Edit and save; these<br>appear on receipts and<br>invoices|
|Settings: Finance and Tax|Currency (TZS), tax rates, payment<br>methods, bank and mobile money<br>accounts, discount limits|Edit and save|
|Settings: Documents|Layout and footer text for receipts,<br>invoices, quotations and payslips;<br>numbering formats|Edit, preview, save|
|Settings: Notifications|Which alerts are on, who receives them,<br>SMS and WhatsApp message templates,<br>reminder days before renewals and due<br>dates|Edit, send a test<br>message|
|Settings: System|Language, date format, session timeout,<br>low-stock default, approval rules|Edit and save|



## **11. Printed and sent documents** 

The system produces six documents: two kinds of receipt, an invoice, a quotation, a payslip and a statement. Each can be printed, downloaded as PDF, or sent by WhatsApp, SMS link or email. Logo, business name, address, phone and TIN come from Settings. 

### **Shop receipt (narrow, for a receipt printer)** 

[LOGO] BUSINESS NAME Address | Phone | TIN -----------------------------Receipt No: R-0001 Date: 15 Nov 2026  Cashier: --Customer: Walk-in 

-----------------------------Item          Qty  Price Total Phone A        1    ---   --- 

Charger        2    ---   -------------------------------TOTAL                      --Paid (Cash)                --Balance                    --- 

Warranty: 12 months (Phone A) Thank you for your business 

|Document|Contents|
|---|---|
|Service receipt|For each payment from a client: receipt number, date,<br>client, project, what the payment is for (deposit,<br>installment, final), amount, method, total paid so far,<br>balance remaining, received by|
|Invoice (A4)|Logo and business details; invoice number, date, due date;<br>client details; table of services with description, quantity,<br>price and total; discount, tax, grand total; amount paid and<br>balance; payment instructions (bank and mobile money<br>numbers); notes and signature area|
|Quotation (A4)|Same layout as the invoice, with a validity date, the services<br>offered, price, terms and conditions, and an acceptance line<br>for the client|
|Payslip|Employee name and position, month, basic salary,<br>allowances, commission, overtime, deductions, advance<br>repayment, net pay, payment method, and date paid|
|Customer or client statement|A list of invoices, payments and balance for a chosen period|



All documents have an automatic number (for example R-0001, INV-0001, Q-0001) that never repeats, and a reprinted copy is marked "Copy". 

## **12. Common behavior on every screen** 

|Topic|Rule|
|---|---|
|Tables|Sortable columns, a search box, filters, 25 rows per page with page<br>numbers, a totals row where amounts are listed, and Export to Excel<br>and PDF|
|Forms|Required fields marked with a star; errors shown in red under the<br>field; Save and Cancel buttons at the bottom; unsaved changes trigger<br>a warning before leaving|
|Amounts|Shown in TZS with thousand separators; entering a negative or zero<br>amount where it makes no sense is blocked|



|Topic|Rule|
|---|---|
|Dates|Day, month and year; default date is today; closed days cannot be<br>edited|
|Status colors|Green for done, paid and approved; yellow for pending and in<br>progress; red for overdue, stuck and rejected; grey for draft and<br>inactive|
|Confirmations|Deleting, voiding, refunding and closing a day ask "Are you sure?" and<br>show what will happen|
|Approvals|Actions that need approval show "Waiting for approval" to the<br>requester, and appear under Approvals on the Dashboard for the<br>approver|
|History|Every record has a History tab showing who created and changed it,<br>with old and new values|
|Attachments|Any record can have photos or files attached, such as receipts and<br>documents|
|Search|Global search in the top bar; each list also has its own search|
|Empty states|A page with no data shows a friendly message and a button to add<br>the first item|
|Loading and errors|A loading indicator while data loads; clear messages if something fails,<br>with a retry button|
|Offline POS|If the internet drops, the POS keeps working, shows an "Offline" label,<br>and syncs the sales when the connection returns|
|Phone view|Sidebar becomes a menu button; tables become cards; the POS stacks<br>the cart under the products; buttons are large enough to tap|
|Speed|Pages should open in under 3 seconds on a normal mobile connection|



## **13. Key workflows step by step** 

These show how the secretary uses the website in a normal day. The developer should test each one from start to finish. 

### **A. New service client, from first call to handover** 

1. Click **+ New > New Client** , fill in the client's details, and save. 

2. On the client page click **Create Quotation** , add the services or a package, and send it by WhatsApp. 

3. When the client agrees, click **Convert to Project** . The stages and the required documents list appear automatically. 

4. Click **Add Payment** for the deposit. The system issues a receipt and creates the invoice balance. 

5. Upload documents as the client brings them; each one is marked Received. 

6. Update each stage as it is completed. Use **Send Update to Client** at key points. 

7. Add costs (government fees, contractors) in the Costs tab as they are paid. 

8. Collect the final payment and mark the last stage done. The project closes, and a renewal reminder is created. 

### **B. Shop sale** 

1. Open **POS / New Sale** . 

2. Scan or tap the products; enter serial numbers for electronics. 

3. Choose the customer, or leave Walk-in. 

4. Apply a discount if needed, choose the payment method, and click **Complete** . 

5. Print the receipt or send it by WhatsApp. Stock and Finance update automatically. 

### **C. Receiving stock from a supplier** 

1. Open **Purchases > New Purchase** and pick the supplier. 

2. Add the products with quantities and costs. 

3. When the goods arrive, click **Receive Goods** . Stock increases. 

4. Record the payment, or leave it as a supplier debt to pay later. 

### **D. End of day** 

1. Open **Finance > End-of-Day Closing** . 

2. Check the expected cash, then count the cash and enter it. 

3. If there is a difference, add a note; then click **Close Day** . 

4. The Owner sees the day's summary on the Dashboard. 

### **E. Month end** 

1. Open **Staff > Payroll** , choose the month, and click **Generate Payroll** . 

2. Review attendance, commission and advance deductions, and fix any line. 

3. The Owner approves, then pays; salary payments post to Finance and payslips are sent. 

4. Open **Reports > Profit and Loss** and **Project Profit** to review the month. 

5. Run a backup and check the Audit Log for unusual changes. 

