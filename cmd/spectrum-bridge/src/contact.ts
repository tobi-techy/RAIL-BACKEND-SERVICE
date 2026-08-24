export interface SharedContact {
  first_name?: string;
  last_name?: string;
  formatted_name?: string;
  phones: string[];
  emails: string[];
  country?: string;
}

const VCARD_MIMES = new Set([
  "text/vcard",
  "text/x-vcard",
  "application/vcard",
  "text/directory",
]);

export function isVCardMime(mime?: string, filename?: string): boolean {
  const m = (mime || "").toLowerCase().split(";")[0].trim();
  if (VCARD_MIMES.has(m)) return true;
  const name = (filename || "").toLowerCase();
  return name.endsWith(".vcf") || name.endsWith(".vcard");
}

type SpectrumContact = {
  name?: {
    first?: string;
    last?: string;
    formatted?: string;
  };
  phones?: Array<{ value?: string } | string>;
  emails?: Array<{ value?: string } | string>;
  addresses?: Array<{ country?: string }>;
};

function values(list?: Array<{ value?: string } | string>): string[] {
  if (!list) return [];
  const out: string[] = [];
  for (const item of list) {
    const v = typeof item === "string" ? item : item?.value;
    if (v && v.trim()) out.push(v.trim());
  }
  return out;
}

export function contactFromSpectrum(content: SpectrumContact): SharedContact {
  return {
    first_name: content.name?.first,
    last_name: content.name?.last,
    formatted_name: content.name?.formatted,
    phones: values(content.phones),
    emails: values(content.emails),
    country: content.addresses?.find((a) => a.country)?.country,
  };
}
