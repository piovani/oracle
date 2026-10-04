# oracle

Histórico de cotações da B3 em Postgres e um comando que monta o plano de operações do dia a partir dele.

## Preparação

```
docker compose up -d
cp .env.example .env
make sync
```

`make sync` baixa os arquivos COTAHIST da B3 e grava as cotações do mercado à vista (lote padrão). Use `make sync SINCE=2020` para limitar o ano inicial.

## Plano de operações

```
make sync
make recommend
make recommend CAPITAL=2000 TOP=5 RISK=2
```

| Variável | Padrão | Significado |
|---|---|---|
| `CAPITAL` | 500 | capital disponível em reais |
| `TOP` | 5 | número máximo de ações no plano |
| `RISK` | 2 | perda máxima por operação, em % do capital |
| `FILL` | — | `FILL=1` completa a lista com sinais mais fracos |

Direto pelo binário há ainda `-years` (anos de histórico, padrão 3) e `-no-estimates` (ignora as estimativas dos analistas).

A saída traz, para cada ação: nome da empresa, força do sinal, quantidade, preço da ordem de compra, preço de venda (alvo), stop, valor investido, ganho no alvo, perda no stop, o desempenho da regra no histórico daquela ação e o preço-alvo dos analistas.

### O que ele faz

- **Histórico:** usa os últimos 3 anos do banco, só ações (ON, PN e units) com volume mediano acima de R$ 5 milhões por dia.
- **Sinal de compra:** ação acima da média de 200 dias que caiu forte nos últimos 2 pregões (reversão à média, RSI de 2 períodos abaixo de 10).
- **Só sinal forte por padrão.** Em muitos dias isso dá menos de 5 ações, ou nenhuma. Com `FILL=1` a lista é completada com níveis mais fracos, nesta ordem: `médio` (RSI < 30), `fraco` (RSI < 50) e `tendência` (acima da média de 200 dias, sem exigir recuo). Em simulação sobre os últimos 3 anos esses níveis não se pagaram.
- **Compra:** ordem limitada 0,5 ATR abaixo do último fechamento, válida só para o pregão seguinte. A ação precisa cair mais um pouco durante o dia para a ordem executar; em cerca de 60% das vezes ela não executa.
- **Venda e stop:** alvo 1 ATR acima do último fechamento, stop 2 ATR abaixo, saída forçada em 5 pregões. Medidos a partir do preço de compra, alvo e stop ficam a 1,5 ATR cada. ATR é a oscilação diária média dos últimos 14 pregões.
- **Estimativas:** preço-alvo e nota de consenso dos analistas vêm do Yahoo Finance. Ação com alvo abaixo do preço atual é descartada. O ranking combina o retorno histórico da regra na ação com o upside estimado.
- **Aporte:** o capital é dividido pelo número de posições e a perda por operação é limitada a `RISK`% do capital. Ações que não cabem na fatia de uma posição ficam fora. Com R$ 500 são R$ 100 por posição e tudo sai no mercado fracionário, por isso o `F` no ticker.

### Como executar o plano

1. Antes da abertura, envie uma ordem de compra limitada no preço indicado, válida só para o dia.
2. Se o leilão de abertura indicar preço no stop ou abaixo, cancele. Se a ordem não executar no dia, descarte.
3. Comprou: deixe a ordem de venda no alvo e o stop no preço indicado.
4. Se nenhum dos dois for atingido em 5 pregões, venda no fechamento.

### Limites

- **A vantagem estatística é pequena.** Seis regras de entrada foram testadas em 2015–2021 e validadas em 2022–2026; todas ficaram perto de zero na validação. A regra adotada foi a única não negativa nos dois períodos. Depois foram testadas quinze variações de saída, entrada e filtros; só a ordem limitada melhorou o resultado nos dois períodos (+0,41% e +0,19% por operação, contra +0,07% e +0,04%).
- **Em simulação dia a dia sobre os últimos 3 anos, só com sinal forte, o retorno médio foi de +0,18% por operação, com 51% de acerto.** A simulação assume que a ordem limitada executa sempre que a mínima do dia toca o preço, e que alvo e stop executam exatamente no preço. No fracionário a execução real tende a ser pior.
- **O retorno por ação é otimista.** Escolher as melhores pelo próprio histórico infla o número.
- **Em reais é pouco.** Com R$ 100 por posição, +0,18% são R$ 0,18 por operação.
- **Não é day trade.** A base tem só dados diários, então o plano é de swing, com até 5 pregões por operação.
- **Yahoo Finance é uma API não oficial.** Se falhar, o comando avisa e ranqueia só pelo histórico.
- **Os preços da B3 não são ajustados por dividendos.** Desdobramentos e grupamentos são detectados e o histórico anterior a eles é descartado.
- **O sufixo `F` é sempre aplicado.** Para quantidades de 100 ou mais, a parte em lotes de 100 deve ir no ticker sem o `F`.

Isto não é recomendação de investimento.
