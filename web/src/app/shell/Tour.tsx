import { useEffect, useState } from "react";
import { Button, Modal } from "@/shared/ui";
import s from "./shell.module.css";

/** Ключ в браузере: знакомство показано (удобство устройства, не данные пользователя). */
const storageKey = "ff.tour";
const startEvent = "ff:tour";

/** Запуск знакомства из меню пользователя. */
export function startTour() {
  window.dispatchEvent(new Event(startEvent));
}

const steps: { title: string; text: string }[] = [
  {
    title: "Добро пожаловать в FreightFlow",
    text: "Мониторинг и анализ логистической инфраструктуры Москвы на открытых данных: объекты, дорожная сеть с ограничениями для грузовиков, маршруты, показатели, оповещения и отчёты.",
  },
  {
    title: "Поиск — Ctrl K",
    text: "Объекты, адреса, улицы, районы, правовые акты и работы из одного поля. Запрос «склады ЮВАО больше 10 000 м² у МКАД» превращается в фильтры реестра.",
  },
  {
    title: "Карта и реестр",
    text: "На карте — слои объектов, ограничений, работ и зон; в разделе «Объекты» — фильтры, карточки с доступностью и выгрузки. Маршрут грузовика строится от любого объекта.",
  },
  {
    title: "Алерты и уведомления",
    text: "Правило следит за работами на вашем маршруте, ограничениями или доступностью объектов. Новые алерты приходят в колокольчик, на почту, в Telegram или на вебхук.",
  },
  {
    title: "Отчёты и справка",
    text: "Паспорт объекта, сводка по району и другие отчёты — в PDF и XLSX, по расписанию на почту. Методики, источники и словарь — в справке (меню пользователя).",
  },
];

function seen(): boolean {
  try {
    return localStorage.getItem(storageKey) === "done";
  } catch {
    return true;
  }
}

function remember() {
  try {
    localStorage.setItem(storageKey, "done");
  } catch {
    // Хранилище недоступно (приватный режим) — знакомство покажется снова.
  }
}

/** Знакомство с системой: при первом входе и по запросу из меню. */
export function Tour() {
  const [open, setOpen] = useState(() => !seen());
  const [step, setStep] = useState(0);
  useEffect(() => {
    const h = () => {
      setStep(0);
      setOpen(true);
    };
    window.addEventListener(startEvent, h);
    return () => window.removeEventListener(startEvent, h);
  }, []);
  const close = () => {
    remember();
    setOpen(false);
  };
  const cur = steps[step] ?? steps[0];
  if (!cur) {
    return null;
  }
  const last = step === steps.length - 1;
  return (
    <Modal
      isOpen={open}
      onOpenChange={(o) => !o && close()}
      title={cur.title}
      actions={
        <>
          <span className={s.tourStep}>
            Шаг {step + 1} из {steps.length}
          </span>
          <Button variant="ghost" onPress={close}>
            {last ? "Закрыть" : "Не сейчас"}
          </Button>
          {step > 0 && <Button onPress={() => setStep((n) => n - 1)}>Назад</Button>}
          {last ? (
            <Button variant="primary" onPress={close}>
              Начать работу
            </Button>
          ) : (
            <Button variant="primary" onPress={() => setStep((n) => n + 1)}>
              Далее
            </Button>
          )}
        </>
      }
    >
      <p>{cur.text}</p>
    </Modal>
  );
}
