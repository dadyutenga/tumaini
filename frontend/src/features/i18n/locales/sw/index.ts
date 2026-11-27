import type { Translations } from "../en";
import common from "./common";
import nav from "./nav";
import auth from "./auth";
import dashboard from "./dashboard";
import pos from "./pos";
import sales from "./sales";
import products from "./products";
import stock from "./stock";
import purchasing from "./purchasing";
import customers from "./customers";
import services from "./services";
import design from "./design";
import finance from "./finance";
import hr from "./hr";
import payroll from "./payroll";
import reports from "./reports";
import approvals from "./approvals";
import notifications from "./notifications";
import admin from "./admin";
import settings from "./settings";
import errors from "./errors";
import status from "./status";

const sw = {
  common,
  nav,
  auth,
  dashboard,
  pos,
  sales,
  products,
  stock,
  purchasing,
  customers,
  services,
  design,
  finance,
  hr,
  payroll,
  reports,
  approvals,
  notifications,
  admin,
  settings,
  errors,
  status,
} satisfies Translations;

export default sw;
