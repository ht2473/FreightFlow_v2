// Тема применяется до отрисовки, чтобы страница не мигала светлым фоном.
(function () {
  try {
    var theme = localStorage.getItem("ff-theme");
    if (theme === "light" || theme === "dark") {
      document.documentElement.setAttribute("data-theme", theme);
    }
  } catch (e) {
    // Хранилище недоступно — используется системная тема.
  }
})();
