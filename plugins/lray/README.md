# Plugin lray para Claude Code

Skill que enseña a Claude a usar [lray](https://github.com/katarem/lray) para todo lo operativo de Liferay: desplegar, arrancar, parar y reiniciar servers, leer y buscar en los logs, comprobar su estado, añadir servers locales o remotos por SSH y crear workspaces y módulos. Así Claude no recurre a `blade`, `gradlew deploy` o `catalina.sh` a mano.

Necesita `lray` instalado y en el `PATH`.

## Instalación

```sh
claude plugin marketplace add katarem/lray
claude plugin install lray@lray
```

O desde Claude Code: `/plugin marketplace add katarem/lray` y `/plugin install lray@lray`.

Para recibir los cambios: `claude plugin marketplace update lray` y `claude plugin update lray@lray`.

La skill está en [`skills/lray/SKILL.md`](skills/lray/SKILL.md) y se mantiene junto al código de lray: cuando cambia un comando u opción, se actualiza en el mismo commit.
