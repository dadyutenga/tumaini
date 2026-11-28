import { useI18n } from "@/features/i18n/i18n";

/** How a thermal receipt will look with these settings. */
export function ReceiptPreview({
  name,
  header,
  footer,
  details = [],
  showLogo = false,
  showCashier = false,
  showTax = false,
  widthClass = "w-72",
}: {
  name: string;
  header?: string;
  footer?: string;
  /** Address, phone and the like, printed under the name. */
  details?: string[];
  showLogo?: boolean;
  showCashier?: boolean;
  showTax?: boolean;
  widthClass?: string;
}) {
  const { t } = useI18n();
  return (
    <div
      data-testid="receipt-preview"
      className={`mx-auto rounded-lg border border-[var(--od-border-strong)] bg-[var(--od-surface)] p-4 font-mono text-[11px] text-[var(--od-ink)] shadow-[var(--od-shadow)] ${widthClass}`}
    >
      <div className="text-center">
        {showLogo && (
          <div className="mx-auto mb-1 grid h-8 w-8 place-items-center rounded bg-zinc-200 text-[9px] font-bold">
            {t.settings.logo}
          </div>
        )}
        <p className="font-bold">{name}</p>
        {header && <p className="text-[10px] text-zinc-600">{header}</p>}
        {details.map((line) => (
          <p key={line} className="text-[10px] text-zinc-600">
            {line}
          </p>
        ))}
      </div>
      <div className="my-2 border-t border-dashed border-zinc-400" />
      <p>{t.settings.receiptInv00042}</p>
      {showCashier && <p>{t.settings.cashierAmina}</p>}
      <div className="my-2 border-t border-dashed border-zinc-400" />
      <div className="flex justify-between">
        <span>{t.settings.n2xSugar1kg}</span>
        <span>6,000</span>
      </div>
      <div className="flex justify-between">
        <span>{t.settings.n1xCookingOil}</span>
        <span>8,500</span>
      </div>
      <div className="my-2 border-t border-dashed border-zinc-400" />
      {showTax && (
        <div className="flex justify-between text-zinc-600">
          <span>VAT (18%)</span>
          <span>2,212</span>
        </div>
      )}
      <div className="flex justify-between font-bold">
        <span>{t.common.total2}</span>
        <span>TZS 14,500</span>
      </div>
      {footer && (
        <>
          <div className="my-2 border-t border-dashed border-zinc-400" />
          <p className="text-center text-[10px] text-zinc-600">{footer}</p>
        </>
      )}
    </div>
  );
}
