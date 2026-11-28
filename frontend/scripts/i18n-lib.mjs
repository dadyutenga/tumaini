/**
 * Finds the strings a user can see in the TypeScript sources. Used by `npm run i18n:check`.
 * It reads the syntax tree, so class names, API paths and comparisons are not mistaken for text.
 */
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";

/** Words that stay in English in every language: codes, product names and units. */
const KEEP_WORDS =
  /\b(?:M-Pesa|TIN|IMEI|SN|PDF|WhatsApp|SMS|SMTP|CSV|XLSX|XLS|TZS|USD|SKU|BMS|NIDA|NSSF|PSSSF|PAYE|SDL|WCF|HESLB|VAT|POS|HR|ID|IP|MFA|QR|URL|KB|MB|GB|TB|PNG|JPG|JPEG|WEBP|ZIP|EFD|OK)\b/g;

/** JSX props whose value is shown (or read out) to the user. */
const TEXT_ATTRIBUTE =
  /^(?:label|title|aria-label|description|empty|placeholder|ariaLabel|trend|subtitle|sub|hint|alt|success|helper|eyebrow|caption|message|error|[a-zA-Z]*(?:Label|Text|Title|Message|Description|Placeholder|Hint))$/;
/** Object properties whose value is shown to the user. */
const TEXT_PROPERTY =
  /^(?:header|label|title|subtitle|description|success|message|placeholder|empty|hint|caption|[a-zA-Z]*(?:Label|Title|Message|Description))$/;
const TEXT_CALL =
  /^(?:toast(?:\.\w+)?|set\w*Error|setMessage|confirm|window\.confirm|alert|window\.alert)$/;
const SKIPPED_ATTRIBUTE =
  /^(?:className|d|viewBox|href|to|src|key|id|type|variant|size|htmlFor|role|rel|target|accept|pattern|autoComplete|inputMode|align|side|stroke|fill|dataKey|stackId|fontFamily|transform|points|strokeDasharray|data-[\w-]+)$|[cC]lassName$|Class$/;

export const hasWords = (text) =>
  /\p{L}{2}/u.test(
    text
      .replace(/\{\{?\w+\}?\}/g, " ")
      .replace(KEEP_WORDS, " ")
      .replace(/\bhttps?:\/\/\S+|\S+@\S+\.\w+|\b[A-Z]{1,4}-\d+\b/g, " "),
  );

export function listSources(root, folders) {
  const out = [];
  const walk = (dir) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const file = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(file);
      else if (/\.tsx?$/.test(file) && !/\.(test|d)\.tsx?$|features[\\/]i18n[\\/]/.test(file))
        out.push(file);
    }
  };
  for (const folder of folders) walk(path.join(root, folder));
  return out.sort();
}

export function createProgram(root) {
  const config = ts.readConfigFile(path.join(root, "tsconfig.json"), ts.sys.readFile);
  const parsed = ts.parseJsonConfigFileContent(config.config, ts.sys, root);
  return ts.createProgram(parsed.fileNames, parsed.options);
}

/** The text React renders for a JSX text node (the same rules Babel applies). */
export function jsxTextValue(raw) {
  const lines = raw.split(/\r\n|\n|\r/);
  let lastWithText = 0;
  lines.forEach((line, i) => {
    if (/[^ \t]/.test(line)) lastWithText = i;
  });
  let out = "";
  lines.forEach((line, i) => {
    let text = line.replace(/\t/g, " ");
    if (i !== 0) text = text.replace(/^ +/, "");
    if (i !== lines.length - 1) text = text.replace(/ +$/, "");
    if (!text) return;
    out += i === lastWithText ? text : `${text} `;
  });
  return decodeEntities(out);
}

const ENTITIES = {
  amp: "&",
  lt: "<",
  gt: ">",
  quot: '"',
  apos: "'",
  nbsp: " ",
  ldquo: "“",
  rdquo: "”",
  lsquo: "‘",
  rsquo: "’",
  mdash: "—",
  ndash: "–",
  middot: "·",
  hellip: "…",
  times: "×",
  rarr: "→",
  larr: "←",
};
const decodeEntities = (text) =>
  text.replace(/&(#x?[0-9a-fA-F]+|\w+);/g, (match, name) => {
    if (name[0] === "#")
      return String.fromCodePoint(
        name[1] === "x" ? parseInt(name.slice(2), 16) : Number(name.slice(1)),
      );
    return ENTITIES[name] ?? match;
  });

const isLiteral = (node) => ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node);

function containsJsx(node) {
  if (ts.isJsxElement(node) || ts.isJsxSelfClosingElement(node) || ts.isJsxFragment(node))
    return true;
  return !!ts.forEachChild(node, (child) => containsJsx(child) || undefined);
}

/** True when a value can be put into a sentence with `fmt`. */
function isPrintable(checker, node) {
  if (containsJsx(node)) return false;
  const type = checker.getTypeAtLocation(node);
  const parts = type.isUnion() ? type.types : [type];
  const allowed =
    ts.TypeFlags.StringLike | ts.TypeFlags.NumberLike | ts.TypeFlags.Undefined | ts.TypeFlags.Null;
  return parts.every((part) => part.flags & allowed);
}

function looksLikeClasses(text) {
  const words = text.trim().split(/\s+/);
  return (
    words.every((w) => /^[!\-\w[\]:/.()%#,&>*=~+'"@|^$]+$/.test(w) && !/^[A-Z]/.test(w)) &&
    words.some((w) => /[-:[\]]/.test(w))
  );
}

/** A literal that reads like a sentence or a label wherever it sits. */
function looksLikeProse(text) {
  const value = text.trim();
  if (!hasWords(value) || looksLikeClasses(value)) return false;
  if (/^[\w.-]+\/[\w./+-]+$|^\/|^#|^--|var\(|^\d+px/.test(value)) return false;
  return (
    /\p{L}{2,}[,.!?:]?(?: | [/&·–—+-] )\p{L}{2,}/u.test(value) ||
    /^\p{Lu}\p{Ll}+(?:-\p{Ll}+)*[….!?]?$/u.test(value)
  );
}

function isIgnoredPosition(node) {
  const parent = node.parent;
  if (!parent) return true;
  if (
    ts.isImportDeclaration(parent) ||
    ts.isExportDeclaration(parent) ||
    ts.isLiteralTypeNode(parent) ||
    ts.isElementAccessExpression(parent) ||
    ts.isCaseClause(parent) ||
    ts.isExternalModuleReference(parent)
  )
    return true;
  if (ts.isPropertyAssignment(parent) && parent.name === node) return true;
  // Directives such as "use client", component display names and header values.
  if (ts.isExpressionStatement(parent)) return true;
  if (ts.isBinaryExpression(parent) && /\.displayName$/.test(parent.left.getText())) return true;
  if (/^Bearer /.test(node.text ?? node.head?.text ?? "")) return true;
  if (
    ts.isBinaryExpression(parent) &&
    [
      ts.SyntaxKind.EqualsEqualsEqualsToken,
      ts.SyntaxKind.ExclamationEqualsEqualsToken,
      ts.SyntaxKind.EqualsEqualsToken,
      ts.SyntaxKind.ExclamationEqualsToken,
      ts.SyntaxKind.InKeyword,
    ].includes(parent.operatorToken.kind)
  )
    return true;
  if (ts.isNewExpression(parent) && parent.expression.getText() === "Error") return true;
  if (ts.isCallExpression(parent)) {
    const callee = parent.expression.getText();
    if (/^(?:cn|cva|clsx|import|require|useWrite|api\.\w+)$/.test(callee)) return true;
    if (
      /\.(?:get|set|has|append|delete|startsWith|endsWith|includes|split|replace|replaceAll|join|querySelector|getElementById|addEventListener|removeEventListener)$/.test(
        callee,
      )
    )
      return true;
  }
  return false;
}

/** `translate="no"` is the HTML way to say a block stays as written, such as a printed receipt. */
const isNotForTranslation = (node) =>
  ts.isJsxElement(node) &&
  node.openingElement.attributes.properties.some((a) => a.getText() === 'translate="no"');

/**
 * Every visible string in one file. A unit is
 * `{ kind, node, nodes, text, parts, context }`, where `parts` are the placeholders of a
 * sentence (`{ name, node, nullable }`).
 */
export function findVisible(sourceFile, checker) {
  const units = [];
  const claimed = new Set();

  const placeholderName = (node, used) => {
    const pick = (n) => {
      if (ts.isIdentifier(n)) return n.text;
      if (ts.isPropertyAccessExpression(n)) return n.name.text === "length" ? "count" : n.name.text;
      if (ts.isParenthesizedExpression(n) || ts.isNonNullExpression(n)) return pick(n.expression);
      if (ts.isBinaryExpression(n)) return pick(n.left);
      if (ts.isCallExpression(n)) {
        if (n.arguments.length) return pick(n.arguments[0]);
        return pick(n.expression);
      }
      return "value";
    };
    const base = (pick(node) || "value").replace(/\W/g, "") || "value";
    let name = base;
    for (let i = 2; used.has(name); i++) name = `${base}${i}`;
    used.add(name);
    return name;
  };

  const isNullable = (node) => {
    const type = checker.getTypeAtLocation(node);
    return (type.isUnion() ? type.types : [type]).some(
      (part) => part.flags & (ts.TypeFlags.Undefined | ts.TypeFlags.Null),
    );
  };

  const addLiteral = (node, context) => {
    if (claimed.has(node)) return;
    claimed.add(node);
    // An object property such as `caption` may hold class names; props and JSX text never do.
    if (hasWords(node.text) && !(context.startsWith("prop:") && looksLikeClasses(node.text)))
      units.push({ kind: "literal", node, text: node.text, parts: [], context });
  };

  const addTemplate = (node, context) => {
    if (claimed.has(node)) return;
    claimed.add(node);
    const fixed = [node.head.text, ...node.templateSpans.map((s) => s.literal.text)];
    for (const span of node.templateSpans) collect(span.expression, context);
    if (!hasWords(fixed.join(" "))) return;
    if (!/^(?:attr:|jsx)/.test(context) && looksLikeClasses(fixed.join("x"))) return;
    const used = new Set();
    const parts = [];
    let text = node.head.text;
    for (const span of node.templateSpans) {
      const name = placeholderName(span.expression, used);
      parts.push({ name, node: span.expression, nullable: isNullable(span.expression) });
      text += `{${name}}${span.literal.text}`;
    }
    units.push({ kind: "template", node, text, parts, context });
  };

  /** Follows the branches of an expression in a visible position down to its literals. */
  function collect(node, context) {
    if (isLiteral(node)) addLiteral(node, context);
    else if (ts.isTemplateExpression(node)) addTemplate(node, context);
    else if (ts.isConditionalExpression(node)) {
      collect(node.whenTrue, context);
      collect(node.whenFalse, context);
    } else if (ts.isBinaryExpression(node)) {
      const op = node.operatorToken.kind;
      if (op === ts.SyntaxKind.AmpersandAmpersandToken) collect(node.right, context);
      else if (
        op === ts.SyntaxKind.BarBarToken ||
        op === ts.SyntaxKind.QuestionQuestionToken ||
        op === ts.SyntaxKind.PlusToken
      ) {
        collect(node.left, context);
        collect(node.right, context);
      }
    } else if (
      ts.isParenthesizedExpression(node) ||
      ts.isAsExpression(node) ||
      ts.isNonNullExpression(node)
    )
      collect(node.expression, context);
    else if (ts.isArrayLiteralExpression(node)) node.elements.forEach((e) => collect(e, context));
  }

  /** Text and values that sit side by side in JSX and read as one sentence. */
  const jsxChildren = (element) => {
    let run = [];
    const flush = () => {
      const items = run;
      run = [];
      const texts = items.filter((i) => ts.isJsxText(i) && hasWords(jsxTextValue(i.text)));
      if (!texts.length) return;
      const values = items.filter(
        (i) => ts.isJsxExpression(i) && i.expression && !isLiteral(i.expression),
      );
      if (!values.length) {
        for (const node of texts)
          units.push({
            kind: "jsxtext",
            node,
            text: jsxTextValue(node.text),
            parts: [],
            context: "jsx",
          });
        return;
      }
      while (items.length && ts.isJsxText(items[0]) && !jsxTextValue(items[0].text)) items.shift();
      while (items.length && ts.isJsxText(items.at(-1)) && !jsxTextValue(items.at(-1).text))
        items.pop();
      const used = new Set();
      const parts = [];
      let text = "";
      for (const item of items) {
        if (ts.isJsxText(item)) text += jsxTextValue(item.text);
        else if (!item.expression) continue;
        else if (isLiteral(item.expression)) {
          claimed.add(item.expression);
          text += item.expression.text;
        } else {
          const name = placeholderName(item.expression, used);
          parts.push({ name, node: item.expression, nullable: isNullable(item.expression) });
          text += `{${name}}`;
        }
      }
      units.push({ kind: "sentence", node: items[0], nodes: items, text, parts, context: "jsx" });
    };
    for (const child of element.children) {
      if (ts.isJsxText(child)) run.push(child);
      else if (
        ts.isJsxExpression(child) &&
        (!child.expression || isPrintable(checker, child.expression))
      )
        run.push(child);
      else flush();
    }
    flush();
  };

  const visit = (node) => {
    if (ts.isImportDeclaration(node) || isNotForTranslation(node)) return;
    if (ts.isJsxAttribute(node)) {
      const name = node.name.getText();
      const element = node.parent.parent.tagName.getText();
      const isSeriesName = name === "name" && /^(?:Area|Bar|Line|Pie)$/.test(element);
      if (SKIPPED_ATTRIBUTE.test(name)) {
        // Still look inside: a class helper may hold JSX, but its literals are never text.
        const skip = (n) => {
          if (isLiteral(n) || ts.isTemplateExpression(n)) claimed.add(n);
          ts.forEachChild(n, skip);
        };
        if (node.initializer) skip(node.initializer);
        return;
      }
      if ((TEXT_ATTRIBUTE.test(name) || isSeriesName) && node.initializer) {
        const value = ts.isJsxExpression(node.initializer)
          ? node.initializer.expression
          : node.initializer;
        if (value) collect(value, `attr:${name}`);
      }
    } else if (ts.isJsxElement(node) || ts.isJsxFragment(node)) jsxChildren(node);
    else if (ts.isJsxExpression(node) && node.expression) {
      if (ts.isJsxElement(node.parent) || ts.isJsxFragment(node.parent))
        collect(node.expression, "jsx");
    } else if (ts.isPropertyAssignment(node)) {
      const name = node.name.getText().replace(/["']/g, "");
      if (TEXT_PROPERTY.test(name)) collect(node.initializer, `prop:${name}`);
    } else if (ts.isCallExpression(node)) {
      const callee = node.expression.getText();
      if (TEXT_CALL.test(callee) && node.arguments[0]) collect(node.arguments[0], `call:${callee}`);
    } else if (ts.isNewExpression(node) && node.expression.getText() === "ApiError") {
      if (node.arguments?.[0]) collect(node.arguments[0], "new:ApiError");
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);

  // Anything else that reads like prose: lookup tables, option lists, default values.
  const sweep = (node) => {
    if (ts.isImportDeclaration(node) || isNotForTranslation(node)) return;
    if (isLiteral(node)) {
      if (!claimed.has(node) && looksLikeProse(node.text) && !isIgnoredPosition(node))
        units.push({ kind: "literal", node, text: node.text, parts: [], context: "other" });
    } else if (ts.isTemplateExpression(node) && !claimed.has(node)) {
      const fixed = [node.head.text, ...node.templateSpans.map((s) => s.literal.text)].join("{}");
      if (looksLikeProse(fixed.replace(/\{\}/g, " ")) && !isIgnoredPosition(node))
        addTemplate(node, "other");
    }
    ts.forEachChild(node, sweep);
  };
  sweep(sourceFile);

  return units.sort((a, b) => a.node.pos - b.node.pos);
}
