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
| `STRICT` | — | `STRICT=1` aceita só sinal forte, mesmo que venham menos ações |

Direto pelo binário há ainda `-years` (anos de histórico, padrão 3) e `-no-estimates` (ignora as estimativas dos analistas).

A saída traz, para cada ação: nome da empresa, força do sinal, quantidade, preço máximo de compra, preço de venda (alvo), stop, valor investido, ganho no alvo, perda no stop, o desempenho da regra no histórico daquela ação e o preço-alvo dos analistas.

### O que ele faz

- **Histórico:** usa os últimos 3 anos do banco, só ações (ON, PN e units) com volume mediano acima de R$ 5 milhões por dia.
- **Sinal de compra:** ação acima da média de 200 dias que recuou nos últimos 2 pregões (reversão à média, medida pelo RSI de 2 períodos).
- **Força do sinal:** a lista começa pelo sinal forte e, se faltarem ações, é completada com níveis mais fracos, nesta ordem: `forte` (RSI < 10), `médio` (RSI < 30), `fraco` (RSI < 50) e `tendência` (acima da média de 200 dias, sem exigir recuo). O histórico de cada ação é calculado com a regra do nível em que ela entrou.
- **Pontos:** a compra vale até 0,5 ATR acima do último fechamento; venda em +1 ATR, stop em −2 ATR, saída forçada em 5 pregões. ATR é a volatilidade média de 14 pregões.
- **Estimativas:** preço-alvo e nota de consenso dos analistas vêm do Yahoo Finance. Ação com alvo abaixo do preço atual é descartada. O ranking combina o retorno histórico da regra na ação com o upside estimado.
- **Aporte:** o capital é dividido pelo número de posições e a perda por operação é limitada a `RISK`% do capital. Ações que não cabem na fatia de uma posição ficam fora. Com R$ 500 são R$ 100 por posição e tudo sai no mercado fracionário, por isso o `F` no ticker.

### Como executar o plano

1. Compre na abertura, somente se o preço estiver acima do stop e até o preço máximo de compra.
2. Deixe a ordem de venda no alvo e o stop no preço indicado.
3. Se nenhum dos dois for atingido em 5 pregões, venda no fechamento.

### Limites

- **A vantagem estatística é mínima.** Seis regras clássicas foram testadas em 2015–2021 e validadas em 2022–2026; todas ficaram perto de zero na validação. A regra adotada foi a única não negativa nos dois períodos (+0,04% por operação na validação). O fim da saída mostra o desempenho de cada nível de sinal em todo o universo, sem seleção.
- **O retorno por ação é otimista.** Escolher as melhores pelo próprio histórico infla o número.
- **Em reais é pouco.** Com R$ 100 por posição, o ganho esperado por operação é de centavos, e o plano arrisca cerca de 2 ATR para ganhar 1.
- **Não é day trade.** A base tem só dados diários, então o plano é de swing, com até 5 pregões por operação.
- **Sinais fracos completam a lista.** Em um dia típico há poucas ações com sinal forte; as demais entram com sinal mais fraco e isso aparece na coluna `SINAL`. Ainda assim pode haver menos ações que o pedido se o mercado estiver abaixo da média de 200 dias.
- **Yahoo Finance é uma API não oficial.** Se falhar, o comando avisa e ranqueia só pelo histórico.
- **Os preços da B3 não são ajustados por dividendos.** Desdobramentos e grupamentos são detectados e o histórico anterior a eles é descartado.
- **O sufixo `F` é sempre aplicado.** Para quantidades de 100 ou mais, a parte em lotes de 100 deve ir no ticker sem o `F`.

Isto não é recomendação de investimento.
