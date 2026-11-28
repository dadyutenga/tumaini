import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import type { Locale } from "@/types";

export interface Translations {
  nav: {
    overview: string;
    records: string;
    reports: string;
    profile: string;
    settings: string;
    example: string;
    home: string;
  };
  groups: Record<string, string>;
  routes: Record<string, string>;
  actions: {
    signIn: string;
    signOut: string;
    save: string;
    cancel: string;
    edit: string;
    delete: string;
    create: string;
    exportCsv: string;
    search: string;
    copy: string;
    print: string;
    previous: string;
    next: string;
    newQuick: string;
    globalSearchPlaceholder: string;
  };
  dashboard: {
    tagline: string;
    title: string;
    subtitle: string;
    totalRecords: string;
    activity: string;
    completed: string;
    pending: string;
    quickActions: string;
    recentActivity: string;
  };
  auth: {
    welcomeBack: string;
    forgotPassword: string;
    resetPassword: string;
    emailLabel: string;
    passwordLabel: string;
    demoNotice: string;
  };
  settings: {
    title: string;
    subtitle: string;
    account: string;
    appearance: string;
    notifications: string;
    language: string;
    theme: string;
  };
}

const dictionaries: Record<Locale, Translations> = {
  en: {
    nav: {
      overview: "Overview",
      records: "Records",
      reports: "Reports",
      profile: "My account",
      settings: "Settings",
      example: "Example page",
      home: "Home",
    },
    groups: {
      Shop: "Shop",
      Services: "Services",
      Finance: "Finance",
      Staff: "Staff",
      Insights: "Insights",
      Administration: "Administration",
      Personal: "Personal",
    },
    routes: {
      "/": "Overview",
      "/pos": "Point of sale",
      "/sales": "Sales",
      "/products": "Products",
      "/stock": "Stock",
      "/purchasing": "Purchasing",
      "/stock-ops": "Transfers and counts",
      "/customers": "Customers",
      "/services": "Services",
      "/clients": "Clients and leads",
      "/projects": "Projects",
      "/service-catalog": "Service catalog",
      "/service-quotations": "Service quotations",
      "/service-invoices": "Service invoices",
      "/design-jobs": "Design jobs",
      "/client-documents": "Client documents",
      "/service-tasks": "Tasks and calendar",
      "/renewals": "Renewals",
      "/contractors": "Contractors",
      "/money": "Money",
      "/debts": "Debts",
      "/day-closing": "Day closing",
      "/expenses": "Expenses",
      "/expense-setup": "Expense setup",
      "/people": "People",
      "/attendance": "Attendance",
      "/hr-setup": "HR setup",
      "/payroll": "Payroll",
      "/payroll-setup": "Payroll setup",
      "/reports": "Reports",
      "/approvals": "Approvals",
      "/notifications": "Notifications",
      "/team": "Team and roles",
      "/audit": "Activity log",
      "/login-history": "Login history",
      "/backups": "Backups",
      "/settings": "Settings",
      "/my-work": "My work",
      "/profile": "My account",
    },
    actions: {
      signIn: "Sign in",
      signOut: "Sign out",
      save: "Save",
      cancel: "Cancel",
      edit: "Edit",
      delete: "Delete",
      create: "Create",
      exportCsv: "Export CSV",
      search: "Search",
      copy: "Copy",
      print: "Print",
      previous: "Previous",
      next: "Next",
      newQuick: "New",
      globalSearchPlaceholder: "Search clients, products, invoices…",
    },
    dashboard: {
      tagline: "BUSINESS OVERVIEW",
      title: "Welcome to your dashboard",
      subtitle: "Track Shop sales, Services projects, cash flow and team tasks in one place.",
      totalRecords: "Total records",
      activity: "Activity",
      completed: "Completed",
      pending: "Pending",
      quickActions: "Quick actions",
      recentActivity: "Recent activity",
    },
    auth: {
      welcomeBack: "Welcome back",
      forgotPassword: "Forgot password",
      resetPassword: "Reset password",
      emailLabel: "Email or phone",
      passwordLabel: "Password",
      demoNotice: "Sign in with your business account.",
    },
    settings: {
      title: "Settings",
      subtitle: "Business profile, branches, finance, documents, notifications and system options.",
      account: "Account",
      appearance: "Appearance",
      notifications: "Notifications",
      language: "Language",
      theme: "Theme",
    },
  },
  sw: {
    nav: {
      overview: "Muhtasari",
      records: "Kumbukumbu",
      reports: "Ripoti",
      profile: "Akaunti yangu",
      settings: "Mipangilio",
      example: "Ukurasa wa mfano",
      home: "Mwanzo",
    },
    groups: {
      Shop: "Duka",
      Services: "Huduma",
      Finance: "Fedha",
      Staff: "Wafanyakazi",
      Insights: "Takwimu",
      Administration: "Utawala",
      Personal: "Binafsi",
    },
    routes: {
      "/": "Muhtasari",
      "/pos": "Kuuza (POS)",
      "/sales": "Mauzo",
      "/products": "Bidhaa",
      "/stock": "Stoo",
      "/purchasing": "Manunuzi",
      "/stock-ops": "Uhamisho na hesabu",
      "/customers": "Wateja wa duka",
      "/services": "Huduma",
      "/clients": "Wateja wa huduma",
      "/projects": "Miradi",
      "/service-catalog": "Orodha ya huduma",
      "/service-quotations": "Nukuu za huduma",
      "/service-invoices": "Ankara za huduma",
      "/design-jobs": "Kazi za ubunifu",
      "/client-documents": "Nyaraka za wateja",
      "/service-tasks": "Kazi na kalenda",
      "/renewals": "Mahuisho",
      "/contractors": "Watoa huduma",
      "/money": "Fedha na akaunti",
      "/debts": "Madeni",
      "/day-closing": "Kufunga siku",
      "/expenses": "Matumizi",
      "/expense-setup": "Mipangilio ya matumizi",
      "/people": "Wafanyakazi",
      "/attendance": "Mahudhurio",
      "/hr-setup": "Mipangilio ya HR",
      "/payroll": "Mishahara",
      "/payroll-setup": "Mipangilio ya mishahara",
      "/reports": "Ripoti",
      "/approvals": "Idhini",
      "/notifications": "Arifa",
      "/team": "Timu na ruhusa",
      "/audit": "Kumbukumbu ya matukio",
      "/login-history": "Historia ya kuingia",
      "/backups": "Nakala za akiba",
      "/settings": "Mipangilio",
      "/my-work": "Kazi zangu",
      "/profile": "Akaunti yangu",
    },
    actions: {
      signIn: "Ingia",
      signOut: "Toka",
      save: "Hifadhi",
      cancel: "Ghairi",
      edit: "Hariri",
      delete: "Futa",
      create: "Unda",
      exportCsv: "Pakua CSV",
      search: "Tafuta",
      copy: "Nakili",
      print: "Chapa",
      previous: "Iliyotangulia",
      next: "Inayofuata",
      newQuick: "Mpya",
      globalSearchPlaceholder: "Tafuta wateja, bidhaa, ankara…",
    },
    dashboard: {
      tagline: "MUHTASARI WA BIASHARA",
      title: "Karibu kwenye dashibodi yako",
      subtitle: "Fuatilia mauzo ya duka, miradi ya huduma, fedha na kazi za timu sehemu moja.",
      totalRecords: "Jumla ya rekodi",
      activity: "Shughuli",
      completed: "Zimekamilika",
      pending: "Zinasubiri",
      quickActions: "Hatua za haraka",
      recentActivity: "Shughuli za hivi karibuni",
    },
    auth: {
      welcomeBack: "Karibu tena",
      forgotPassword: "Umesahau nenosiri",
      resetPassword: "Weka nenosiri upya",
      emailLabel: "Barua pepe au simu",
      passwordLabel: "Nenosiri",
      demoNotice: "Ingia kwa akaunti yako ya biashara.",
    },
    settings: {
      title: "Mipangilio",
      subtitle: "Taarifa za biashara, matawi, fedha, nyaraka, arifa na mipangilio ya mfumo.",
      account: "Akaunti",
      appearance: "Muonekano",
      notifications: "Arifa",
      language: "Lugha",
      theme: "Mandhari",
    },
  },
};

interface I18nContextType {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: Translations;
  routeLabel: (path: string, fallback: string) => string;
  groupLabel: (group: string) => string;
}

const makeHelpers = (locale: Locale) => ({
  routeLabel: (path: string, fallback: string) => dictionaries[locale].routes[path] ?? fallback,
  groupLabel: (group: string) => dictionaries[locale].groups[group] ?? group,
});

const defaultI18nValue: I18nContextType = {
  locale: "en",
  setLocale: () => {},
  t: dictionaries.en,
  ...makeHelpers("en"),
};

const I18nContext = createContext<I18nContextType | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(() => {
    try {
      const saved = localStorage.getItem("template-locale");
      return saved === "sw" ? "sw" : "en";
    } catch {
      return "en";
    }
  });

  const setLocale = (next: Locale) => {
    setLocaleState(next);
    try {
      localStorage.setItem("template-locale", next);
    } catch {
      /* ignore */
    }
  };

  useEffect(() => {
    if (typeof document !== "undefined") {
      document.documentElement.lang = locale;
    }
  }, [locale]);

  const helpers = makeHelpers(locale);

  return (
    <I18nContext.Provider value={{ locale, setLocale, t: dictionaries[locale], ...helpers }}>
      {children}
    </I18nContext.Provider>
  );
}

export function useI18n() {
  const context = useContext(I18nContext);
  return context ?? defaultI18nValue;
}
