import { z } from "zod";

/** Состояние раздела в адресе: вкладка, уровень и показатель, индекс и его уровень, ряд. */
export const analyticsSearchSchema = z.object({
  tab: z.enum(["metrics", "indexes", "series", "spatial"]).optional().catch(undefined),
  lv: z.enum(["district", "okrug", "h3_8", "h3_9", "facility", "segment"]).optional().catch(undefined),
  m: z.string().max(80).optional().catch(undefined),
  ix: z.string().max(80).optional().catch(undefined),
  il: z.enum(["district", "h3_8"]).optional().catch(undefined),
  sm: z.string().max(80).optional().catch(undefined),
});

export type AnalyticsSearch = z.infer<typeof analyticsSearchSchema>;
